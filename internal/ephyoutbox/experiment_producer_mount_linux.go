//go:build linux

package ephyoutbox

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type producerMount struct {
	id     uint64
	device string
	root   string
	point  string
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
		result = append(result, producerMount{id: id, device: fmt.Sprintf("%d:%d", major, minor), root: root, point: mountPoint})
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

func producerFileMountIdentity(file *os.File) (uint64, string, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, "", err
	}
	fdInfo, err := producerProcFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd()), 16*1024)
	if err != nil {
		return 0, "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, "", fmt.Errorf("producer filesystem identity is unavailable")
	}
	var id uint64
	for _, line := range strings.Split(string(fdInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			if id != 0 {
				return 0, "", fmt.Errorf("duplicate producer mount identity")
			}
			id, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil || id == 0 {
				return 0, "", fmt.Errorf("invalid producer mount identity")
			}
		}
	}
	if id == 0 {
		return 0, "", fmt.Errorf("missing producer mount identity")
	}
	return id, fmt.Sprintf("%d:%d", unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))), nil
}

func producerRootMountIdentity(root *os.Root) (uint64, string, error) {
	file, err := root.Open(".")
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	return producerFileMountIdentity(file)
}

func producerCurrentMounts() ([]producerMount, error) {
	raw, err := producerProcFile("/proc/self/mountinfo", 1024*1024)
	if err != nil {
		return nil, err
	}
	return parseProducerMounts(raw)
}

func validateProducerMountAliases(roots ...*os.Root) error {
	mounts, err := producerCurrentMounts()
	if err != nil {
		return err
	}
	for _, root := range roots {
		id, device, err := producerRootMountIdentity(root)
		if err != nil {
			return err
		}
		if err := validateProducerMountSelection(mounts, id, device); err != nil {
			return err
		}
	}
	return nil
}

func validateProducerWriteMountSelection(mounts []producerMount, id uint64, device, rootPath string) error {
	if err := validateProducerMountSelection(mounts, id, device); err != nil {
		return err
	}
	managed := filepath.Join(rootPath, ".mdsys")
	for _, mount := range mounts {
		inside, err := filepath.Rel(managed, mount.point)
		if err != nil {
			return err
		}
		if inside == "." || (inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))) {
			return fmt.Errorf("producer managed tree contains a descendant mount; unsupported write target")
		}
	}
	return nil
}

func validateProducerWriteTopology(root *os.Root) error {
	mounts, err := producerCurrentMounts()
	if err != nil {
		return err
	}
	id, device, err := producerRootMountIdentity(root)
	if err != nil {
		return err
	}
	rootPath, err := producerRootPath(root)
	if err != nil {
		return err
	}
	return validateProducerWriteMountSelection(mounts, id, device, rootPath)
}

func validateProducerWriteRoot(root, target *os.Root) error {
	if err := validateProducerWriteTopology(root); err != nil {
		return err
	}
	id, device, err := producerRootMountIdentity(root)
	if err != nil {
		return err
	}
	targetID, targetDevice, err := producerRootMountIdentity(target)
	if err != nil {
		return err
	}
	if targetID != id || targetDevice != device {
		return fmt.Errorf("producer write directory differs from retained data mount")
	}
	return nil
}

func validateProducerWriteFile(root *os.Root, file *os.File) error {
	if err := validateProducerWriteTopology(root); err != nil {
		return err
	}
	id, device, err := producerRootMountIdentity(root)
	if err != nil {
		return err
	}
	targetID, targetDevice, err := producerFileMountIdentity(file)
	if err != nil {
		return err
	}
	if targetID != id || targetDevice != device {
		return fmt.Errorf("producer lock file differs from retained data mount")
	}
	return nil
}
