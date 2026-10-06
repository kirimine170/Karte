//go:build linux

package ephyoutbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProducerMountAliasesFailClosed(t *testing.T) {
	const original = "10 1 8:1 / / rw - ext4 /dev/root rw\n"
	for _, tc := range []struct {
		name   string
		table  string
		id     uint64
		device string
		accept bool
	}{
		{"full-filesystem", original, 10, "8:1", true},
		{"independent-full-filesystem", original + "20 10 8:2 / /mnt/data rw - ext4 /dev/data rw\n", 20, "8:2", true},
		{"bound-source", original + "11 10 8:1 /data/.mdsys /mnt/worker rw - ext4 /dev/root rw\n", 11, "8:1", false},
		{"data-with-bound-source", original + "11 10 8:1 /data/.mdsys /mnt/worker rw - ext4 /dev/root rw\n", 10, "8:1", false},
		{"bound-managed-child", original + "11 10 8:1 /worker /data/.mdsys/ephy/experiments rw - ext4 /dev/root rw\n", 10, "8:1", false},
		{"bound-whole-filesystem", original + "11 10 8:1 / /mnt/alias rw - ext4 /dev/root rw\n", 11, "8:1", false},
		{"hidden-original-bind", "11 1 8:1 /data/.mdsys /mnt/worker rw - ext4 /dev/root rw\n", 11, "8:1", false},
		{"subvolume", "11 1 8:1 /@home /home rw - btrfs /dev/root rw\n", 11, "8:1", false},
		{"unrelated-device-alias", original + "20 10 8:2 /unrelated /mnt/other rw - ext4 /dev/other rw\n", 10, "8:1", true},
		{"unrelated-namespace-filesystem", original + "20 10 0:4 mnt:[4026532913] /run/snapd/ns/lxd.mnt rw - nsfs nsfs rw\n", 10, "8:1", true},
		{"selected-namespace-filesystem", original + "20 10 0:4 mnt:[4026532913] /run/snapd/ns/lxd.mnt rw - nsfs nsfs rw\n", 20, "0:4", false},
		{"noncanonical-selected-root", "11 1 8:1 /a/../b /mnt/worker rw - ext4 /dev/root rw\n", 11, "8:1", false},
		{"missing-mount", original, 99, "8:1", false},
		{"device-mismatch", original, 10, "8:2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mounts, err := parseProducerMounts([]byte(tc.table))
			if err != nil {
				t.Fatal(err)
			}
			err = validateProducerMountSelection(mounts, tc.id, tc.device)
			if (err == nil) != tc.accept {
				t.Fatalf("mount alias acceptance=%v, want %v (%v)", err == nil, tc.accept, err)
			}
		})
	}
}

func TestProducerMountInformationStrictAndBounded(t *testing.T) {
	valid := "10 1 8:1 / / rw shared:3 unknown:4 - ext4 /dev/root rw\n"
	if _, err := parseProducerMounts([]byte(valid)); err != nil {
		t.Fatal("unknown optional fields must be accepted:", err)
	}
	for _, raw := range []string{
		"", "10 1 8:1 / / rw ext4 /dev/root rw", valid + valid,
		"0 1 8:1 / / rw - ext4 /dev/root rw",
		"10 bad 8:1 / / rw - ext4 /dev/root rw",
		"10 1 bad / / rw - ext4 /dev/root rw",
		"10 1 8:bad / / rw - ext4 /dev/root rw",
		"10 1 8:1 / relative rw - ext4 /dev/root rw",
		"10 1 8:1 /bad\\999 / rw - ext4 /dev/root rw",
		"10 1 8:1 / /bad\\0 rw - ext4 /dev/root rw",
		"10 1 8:1 / / rw - ext4 /dev/root rw unexpected",
	} {
		if _, err := parseProducerMounts([]byte(raw)); err == nil {
			t.Fatalf("ambiguous mount information accepted: %q", raw)
		}
	}
	decoded, err := producerMountPath(`/space\040tab\011newline\012slash\134`)
	if err != nil || decoded != "/space tab\tnewline\nslash\\" {
		t.Fatalf("mount path escape decoding: %q %v", decoded, err)
	}
	if _, err := producerProcFile("/proc/self/mountinfo", 1); err == nil {
		t.Fatal("mount information allocation was not bounded")
	}
}

func TestProducerWriteMountsRejectManagedDescendants(t *testing.T) {
	const original = "10 1 8:1 / / rw - ext4 /dev/root rw\n"
	for _, point := range []string{
		"/data/.mdsys", "/data/.mdsys/ephy", "/data/.mdsys/ephy/outbox",
		"/data/.mdsys/ephy/outbox/pending", "/data/.mdsys/ephy/outbox/pending/candidate.json",
		"/data/.mdsys/ephy/experiment-producer/.locks",
		"/data/.mdsys/ephy/experiment-producer/.locks/candidate.lock",
		"/data/.mdsys/ephy/experiments/worker/artifacts",
	} {
		t.Run(point, func(t *testing.T) {
			// The extra mount uses a different device, so the old root-only
			// validation permits it even though writes would enter a submount.
			mounts, err := parseProducerMounts([]byte(original + "20 10 8:2 /worker " + point + " rw - ext4 /dev/worker rw\n"))
			if err != nil {
				t.Fatal(err)
			}
			if err := validateProducerMountSelection(mounts, 10, "8:1"); err != nil {
				t.Fatal("root-only check should demonstrate the old gap:", err)
			}
			if err := validateProducerWriteMountSelection(mounts, 10, "8:1", "/data"); err == nil {
				t.Fatal("managed descendant mount accepted as a write target")
			}
		})
	}
	for _, point := range []string{"/data/.mdsys-other", "/other/.mdsys/ephy/outbox"} {
		mounts, err := parseProducerMounts([]byte(original + "20 10 8:2 /worker " + point + " rw - ext4 /dev/worker rw\n"))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateProducerWriteMountSelection(mounts, 10, "8:1", "/data"); err != nil {
			t.Fatal("disjoint mount should not forbid the managed tree:", err)
		}
	}
}

func TestProducerWriteHandlesRequireDataMount(t *testing.T) {
	data := t.TempDir()
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Mkdir(filepath.Join(data, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	child, err := root.OpenRoot("child")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err := validateProducerWriteRoot(root, child); err != nil {
		t.Fatal("same-mount directory refused:", err)
	}
	file, err := child.OpenFile("lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateProducerWriteFile(root, file); err != nil {
		t.Fatal("same-mount file refused:", err)
	}
	// /proc already exists as a different mount in the test environment. This
	// checks actual directory/file fd identity without creating any mount.
	other, err := os.OpenRoot("/proc")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := validateProducerWriteRoot(root, other); err == nil {
		t.Fatal("different actual directory mount accepted")
	}
	procFile, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer procFile.Close()
	if err := validateProducerWriteFile(root, procFile); err == nil {
		t.Fatal("different actual file mount accepted")
	}
	if _, err := os.Lstat(filepath.Join(data, ".mdsys")); !os.IsNotExist(err) {
		t.Fatal("read-only mount checks created managed output")
	}
}
