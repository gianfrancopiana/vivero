package vivero

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

var (
	errProcessNotRunning      = errors.New("process is not running")
	errProcessIdentityMissing = errors.New("process identity was not recorded")
	errProcessIdentityChanged = errors.New("process identity changed")
)

// processIdentity returns a boot-scoped process start token. Unlike a PID, the
// token changes when the operating system recycles a process slot.
func processIdentity(pid int) (string, error) {
	if pid <= 0 {
		return "", errProcessNotRunning
	}
	if runtime.GOOS == "linux" {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			if os.IsNotExist(err) {
				return "", errProcessNotRunning
			}
			return "", err
		}
		// The second field is parenthesized and may contain spaces. Fields after
		// the final ')' begin at process state (field 3); starttime is field 22.
		closeParen := strings.LastIndexByte(string(stat), ')')
		if closeParen < 0 {
			return "", fmt.Errorf("parse process stat for pid %d", pid)
		}
		fields := strings.Fields(string(stat)[closeParen+1:])
		if len(fields) <= 19 {
			return "", fmt.Errorf("parse process start token for pid %d", pid)
		}
		bootID, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		return "linux:" + strings.TrimSpace(string(bootID)) + ":" + fields[19], nil
	}

	out, err := runCmd("", nil, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", errProcessNotRunning
	}
	return runtime.GOOS + ":" + strings.Join(strings.Fields(string(out)), " "), nil
}

func killTrackedProcessGroup(pid int, expectedIdentity string) error {
	if pid <= 0 {
		return nil
	}
	current, err := processIdentity(pid)
	if errors.Is(err, errProcessNotRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify pid %d identity: %w", pid, err)
	}
	if strings.TrimSpace(expectedIdentity) == "" {
		return fmt.Errorf("%w: refusing to signal pid %d", errProcessIdentityMissing, pid)
	}
	if current != expectedIdentity {
		return fmt.Errorf("%w: refusing to signal recycled pid %d", errProcessIdentityChanged, pid)
	}
	return killProcessGroup(pid)
}
