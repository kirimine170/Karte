//go:build linux

package ephyoutbox

import (
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
		"10 1 8:1 relative / rw - ext4 /dev/root rw",
		"10 1 8:1 /a/../b / rw - ext4 /dev/root rw",
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
