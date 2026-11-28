package codex

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	SupervisorArgument = "--internal-codex-supervisor"
	// Linux amd64 and arm64 use the same syscall numbers.
	pidfdOpen       = 434
	pidfdSendSignal = 424
	pPIDFD          = 3
	cleanupTimeout  = 5 * time.Second
)

func openProcessFD(pid int) (int, error) {
	fd, _, errno := syscall.Syscall(pidfdOpen, uintptr(pid), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func signalProcessFD(fd int, signal syscall.Signal) error {
	_, _, errno := syscall.Syscall6(pidfdSendSignal, uintptr(fd), uintptr(signal), 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// childFD verifies kernel parentage without consuming the exit status. A PID
// from procfs might already have been reused when pidfd_open runs.
func childFD(fd int) error {
	var siginfo [16]uint64 // Linux siginfo_t is 128 bytes, naturally aligned.
	_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, pPIDFD, uintptr(fd),
		uintptr(unsafe.Pointer(&siginfo)), syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT|syscall.WALL, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// Supervise is the internal executable entry used for one app-server. A
// dedicated subreaper never inherits other work from the workflow process.
func Supervise(argv []string) error {
	control := os.NewFile(3, "controller")
	defer control.Close()
	info, err := control.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 || len(argv) == 0 {
		return errors.New("Codex supervisor requires its private controller pipe and app-server executable")
	}
	syscall.CloseOnExec(3)
	if err := prepareSubreaper(); err != nil {
		return fmt.Errorf("establish Codex process boundary: %w", err)
	}
	stop := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, control)
		close(stop)
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	// Do not inherit SIG_IGN for SIGCHLD: Linux would discard wait status.
	childSignals := make(chan os.Signal, 1)
	signal.Notify(childSignals, syscall.SIGCHLD)
	defer signal.Stop(childSignals)
	select {
	case <-stop:
		return nil
	case <-signals:
		return nil
	default:
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start supervised Codex app-server: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-stop:
	case <-signals:
	}
	deadline := time.Now().Add(cleanupTimeout)
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop Codex app-server: %w", err)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		return errors.New("Codex app-server did not stop")
	}
	return drainChildren(deadline)
}

func prepareSubreaper() error {
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(os.Getpid()) {
		return errors.New("procfs does not expose the supervisor's PID namespace")
	}
	fd, err := openProcessFD(os.Getpid())
	if err != nil {
		return fmt.Errorf("pidfd_open: %w", err)
	}
	defer syscall.Close(fd)
	if err := signalProcessFD(fd, 0); err != nil {
		return fmt.Errorf("pidfd_send_signal: %w", err)
	}
	if err := childFD(fd); !errors.Is(err, syscall.ECHILD) {
		return fmt.Errorf("unexpected waitid on supervisor (want ECHILD): %v", err)
	}
	if _, err := os.ReadFile("/proc/thread-self/children"); err != nil {
		return fmt.Errorf("inspect supervisor children: %w", err)
	}
	const setChildSubreaper = 36 // PR_SET_CHILD_SUBREAPER
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, setChildSubreaper, 1, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func drainChildren(deadline time.Time) error {
	for time.Now().Before(deadline) {
		// There is no other waiter once app-server Wait completes. ECHILD
		// proves no children remain, including any reparented grandchildren.
		pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG|syscall.WALL, nil)
		switch {
		case errors.Is(err, syscall.ECHILD):
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case err != nil:
			return fmt.Errorf("reap Codex descendants: %w", err)
		case pid != 0:
			continue
		}
		if err := stopChildren(); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("Codex descendants did not stop and reap before the cleanup deadline")
}

func stopChildren() error {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		data, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue // A runtime thread exited; a later sweep sees its adoptees.
		}
		if err != nil {
			return err
		}
		for _, raw := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(raw)
			if err != nil || pid <= 0 {
				return fmt.Errorf("invalid procfs child PID %q", raw)
			}
			if err := stopChild(pid); err != nil {
				return fmt.Errorf("stop Codex descendant %d: %w", pid, err)
			}
		}
	}
	return nil
}

func stopChild(pid int) error {
	fd, err := openProcessFD(pid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	if err := childFD(fd); errors.Is(err, syscall.ECHILD) {
		return nil
	} else if err != nil {
		return err
	}
	err = signalProcessFD(fd, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
