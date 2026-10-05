//go:build linux

package ephyoutbox

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type producerMount struct {
	id     uint64
	device string
	root   string
}

func producerProcFile(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("producer proc information exceeds limit")
	}
	return raw, nil
}

func producerMountPath(encoded string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(encoded); i++ {
		if encoded[i] != '\\' {
			result.WriteByte(encoded[i])
			continue
		}
		if i+3 >= len(encoded) {
			return "", fmt.Errorf("invalid mount path escape")
		}
		decoded, ok := map[string]byte{"040": ' ', "011": '\t', "012": '\n', "134": '\\'}[encoded[i+1:i+4]]
		if !ok {
			return "", fmt.Errorf("invalid mount path escape")
		}
		result.WriteByte(decoded)
		i += 3
	}
	name := result.String()
	// Some kernel pseudo filesystems supply a dentry name such as
	// "mnt:[4026531840]" rather than an absolute root path. Preserve it:
	// selected producer roots still require the exact full-fs root "/".
	if name == "" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid mount path")
	}
	return name, nil
}

// Linux mountinfo identifies the filesystem root behind a bind mount; a
// retained fd pathname alone identifies only its namespace view. See
// https://man7.org/linux/man-pages/man5/proc_pid_mountinfo.5.html.
func parseProducerMounts(raw []byte) ([]producerMount, error) {
	var result []producerMount
	seen := map[uint64]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("invalid producer mount information")
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			return nil, fmt.Errorf("invalid producer mount separator")
		}
		id, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || id == 0 || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate producer mount identity")
		}
		seen[id] = true
		if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
			return nil, fmt.Errorf("invalid producer parent mount identity")
		}
		device := strings.Split(fields[2], ":")
		if len(device) != 2 {
			return nil, fmt.Errorf("invalid producer mount device")
		}
		major, majorErr := strconv.ParseUint(device[0], 10, 32)
		minor, minorErr := strconv.ParseUint(device[1], 10, 32)
		if majorErr != nil || minorErr != nil {
			return nil, fmt.Errorf("invalid producer mount device")
		}
		root, err := producerMountPath(fields[3])
		if err != nil {
			return nil, err
		}
		mountPoint, err := producerMountPath(fields[4])
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(mountPoint, "/") {
			return nil, fmt.Errorf("invalid producer mount point")
		}
		result = append(result, producerMount{id: id, device: fmt.Sprintf("%d:%d", major, minor), root: root})
	}
	return result, nil
}

// Synthetic producer v1 deliberately supports only a uniquely mounted full
// filesystem for each root. Bind mounts, subvolume roots, and multiple mounts
// of that filesystem fail closed rather than claiming path-based containment.
func validateProducerMountSelection(mounts []producerMount, id uint64, device string) error {
	selected := -1
	count := 0
	for i, mount := range mounts {
		if mount.id == id {
			selected = i
		}
		if mount.device == device {
			count++
		}
	}
	if selected < 0 || mounts[selected].device != device {
		return fmt.Errorf("producer root mount identity is unavailable or inconsistent")
	}
	if mounts[selected].root != "/" || count != 1 {
		return fmt.Errorf("producer does not support Linux mount aliases or subvolume roots; use a uniquely mounted full filesystem")
	}
	return nil
}

func validateProducerMountAliases(roots ...*os.Root) error {
	raw, err := producerProcFile("/proc/self/mountinfo", 1024*1024)
	if err != nil {
		return err
	}
	mounts, err := parseProducerMounts(raw)
	if err != nil {
		return err
	}
	for _, root := range roots {
		file, err := root.Open(".")
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		fdInfo, readErr := producerProcFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd()), 16*1024)
		file.Close()
		if statErr != nil {
			return statErr
		}
		if readErr != nil {
			return readErr
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("producer root filesystem identity is unavailable")
		}
		var id uint64
		for _, line := range strings.Split(string(fdInfo), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "mnt_id:" {
				if id != 0 {
					return fmt.Errorf("duplicate producer root mount identity")
				}
				id, err = strconv.ParseUint(fields[1], 10, 64)
				if err != nil || id == 0 {
					return fmt.Errorf("invalid producer root mount identity")
				}
			}
		}
		device := fmt.Sprintf("%d:%d", unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev)))
		if err := validateProducerMountSelection(mounts, id, device); err != nil {
			return err
		}
	}
	return nil
}
