package main

import (
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"
)

type commandInvocation struct {
	name string
	args []string
}

func TestStartRoleServicesUsesRoleSpecificUnits(t *testing.T) {
	tests := []struct {
		name              string
		role              string
		publicObservation bool
		want              []commandInvocation
	}{
		{
			name: "service node",
			role: "node-2",
			want: []commandInvocation{
				{name: "systemctl", args: []string{"daemon-reload"}},
				{name: "systemctl", args: []string{"enable", "--now", "node-exporter.service", "wukongim.service", "wukongim-cgroup-metrics.service"}},
			},
		},
		{
			name:              "simulator with public observation",
			role:              "sim",
			publicObservation: true,
			want: []commandInvocation{
				{name: "systemctl", args: []string{"daemon-reload"}},
				{name: "systemctl", args: []string{"enable", "--now", "node-exporter.service", "prometheus.service", "wkbench-worker.service", "wkanalysis.service", "wkcloudview.service"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls []commandInvocation
			run := func(name string, args ...string) error {
				calls = append(calls, commandInvocation{name: name, args: append([]string(nil), args...)})
				return nil
			}
			if err := startRoleServicesWithRunner(test.role, test.publicObservation, run); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, test.want) {
				t.Fatalf("commands = %#v, want %#v", calls, test.want)
			}
		})
	}
}

func TestStartRoleServicesStopsOnCommandFailure(t *testing.T) {
	reloadErr := errors.New("reload failed")
	var calls int
	err := startRoleServicesWithRunner("node-1", false, func(string, ...string) error {
		calls++
		return reloadErr
	})
	if !errors.Is(err, reloadErr) || calls != 1 {
		t.Fatalf("reload failure = %v, calls = %d", err, calls)
	}

	enableErr := errors.New("enable failed")
	calls = 0
	err = startRoleServicesWithRunner("sim", false, func(string, ...string) error {
		calls++
		if calls == 2 {
			return enableErr
		}
		return nil
	})
	if !errors.Is(err, enableErr) || calls != 2 {
		t.Fatalf("enable failure = %v, calls = %d", err, calls)
	}
}

func TestEnsureServiceUserIsIdempotent(t *testing.T) {
	created := false
	err := ensureServiceUserWithRunner(
		func(name string, args ...string) error {
			if name != "id" || !reflect.DeepEqual(args, []string{"-u", "wukongim"}) {
				t.Fatalf("probe = %s %v", name, args)
			}
			return nil
		},
		func(string, ...string) error {
			created = true
			return nil
		},
	)
	if err != nil || created {
		t.Fatalf("existing user result = %v, created = %v", err, created)
	}

	createErr := errors.New("useradd failed")
	var create commandInvocation
	err = ensureServiceUserWithRunner(
		func(string, ...string) error { return errors.New("not found") },
		func(name string, args ...string) error {
			create = commandInvocation{name: name, args: append([]string(nil), args...)}
			return createErr
		},
	)
	want := commandInvocation{name: "useradd", args: []string{"--system", "--home", "/var/lib/wukongim-cloud", "--shell", "/usr/sbin/nologin", "wukongim"}}
	if !errors.Is(err, createErr) || !reflect.DeepEqual(create, want) {
		t.Fatalf("create result = %v, command = %#v", err, create)
	}
}

func TestPrepareDataDiskRejectsUnsafeDeviceBeforeHostAccess(t *testing.T) {
	err := prepareDataDisk("relative-disk")
	if err == nil || !strings.Contains(err.Error(), "under /dev") {
		t.Fatalf("prepareDataDisk(relative) error = %v", err)
	}
}

func TestPrepareDataDiskStateMachine(t *testing.T) {
	t.Run("requires an existing device", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.statErr = fs.ErrNotExist
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "existing block device") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("rejects a mount owned by another path", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.findmnt = outputResult{output: []byte("/mnt/other\n")}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "/mnt/other") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates mount inspection failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.findmnt = outputResult{err: errors.New("findmnt failed")}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "inspect data device mount") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("mounted target only reconciles fstab", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.findmnt = outputResult{output: []byte("/var/lib/wukongim-cloud\n")}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err != nil {
			t.Fatal(err)
		}
		if len(system.runCalls) != 0 || system.appendedPath != "/etc/fstab" || !strings.HasPrefix(system.appendedEntry, "UUID=disk-uuid ") {
			t.Fatalf("run calls = %#v, fstab = %q %q", system.runCalls, system.appendedPath, system.appendedEntry)
		}
	})

	t.Run("formats an empty device before mounting", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.filesystems = []outputResult{{err: exitStatusError(2)}, {output: []byte("ext4\n")}}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err != nil {
			t.Fatal(err)
		}
		want := []commandInvocation{
			{name: "mkfs.ext4", args: []string{"-F", "/dev/test"}},
			{name: "mount", args: []string{"-o", "nodev,nosuid,noatime", "/dev/test", "/var/lib/wukongim-cloud"}},
		}
		if !reflect.DeepEqual(system.runCalls, want) || system.appendedPath != "/etc/fstab" {
			t.Fatalf("run calls = %#v, fstab path = %q", system.runCalls, system.appendedPath)
		}
	})

	t.Run("rejects an unexpected filesystem", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.filesystems = []outputResult{{output: []byte("xfs\n")}}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "unexpected filesystem") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates filesystem inspection failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.filesystems = []outputResult{{err: exitStatusError(3)}}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "inspect data device filesystem") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates format failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.filesystems = []outputResult{{err: exitStatusError(2)}}
		system.runErrors["mkfs.ext4"] = errors.New("format failed")
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || err.Error() != "format failed" {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("verifies the filesystem after format", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.filesystems = []outputResult{{err: exitStatusError(2)}, {err: errors.New("verification failed")}}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "verify data device filesystem") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("requires an empty mountpoint", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.entries = []fs.DirEntry{fakeDirEntry{name: "existing-data"}}
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || !strings.Contains(err.Error(), "mount point must be empty") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates mountpoint creation failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.mkdirErr = errors.New("mkdir failed")
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || err.Error() != "mkdir failed" {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates mount failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.runErrors["mount"] = errors.New("mount failed")
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || err.Error() != "mount failed" {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates fstab failure", func(t *testing.T) {
		system := newFakeDataDiskSystem()
		system.appendErr = errors.New("fstab failed")
		if err := prepareDataDiskWithOps("/dev/test", system.ops()); err == nil || err.Error() != "fstab failed" {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestMountedAtClassifiesCommandResults(t *testing.T) {
	mount, mounted, err := mountedAt("/dev/test", func(string, ...string) ([]byte, error) {
		return nil, exitStatusError(1)
	})
	if err != nil || mounted || mount != "" {
		t.Fatalf("unmounted = %q, %v, %v", mount, mounted, err)
	}
	if _, _, err := mountedAt("/dev/test", func(string, ...string) ([]byte, error) {
		return []byte(" \n"), nil
	}); err == nil || !strings.Contains(err.Error(), "empty target") {
		t.Fatalf("empty target error = %v", err)
	}
	commandErr := errors.New("findmnt unavailable")
	if _, _, err := mountedAt("/dev/test", func(string, ...string) ([]byte, error) {
		return nil, commandErr
	}); !errors.Is(err, commandErr) {
		t.Fatalf("command error = %v", err)
	}
}

func TestEnsureDataDiskFstabRequiresUUID(t *testing.T) {
	err := ensureDataDiskFstab(
		"/dev/test",
		func(string, ...string) ([]byte, error) { return []byte(" \n"), nil },
		func(string, string) error { t.Fatal("append called without UUID"); return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "UUID is unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestActivateOfflineUnitsStopsOnFailure(t *testing.T) {
	reloadErr := errors.New("reload failed")
	var calls int
	err := activateOfflineUnitsWithRunner("service-1", func(string, ...string) error {
		calls++
		return reloadErr
	})
	if !errors.Is(err, reloadErr) || calls != 1 {
		t.Fatalf("reload failure = %v, calls = %d", err, calls)
	}

	enableErr := errors.New("enable failed")
	calls = 0
	err = activateOfflineUnitsWithRunner("service-1", func(string, ...string) error {
		calls++
		if calls == 2 {
			return enableErr
		}
		return nil
	})
	if !errors.Is(err, enableErr) || calls != 2 {
		t.Fatalf("enable failure = %v, calls = %d", err, calls)
	}
}

type outputResult struct {
	output []byte
	err    error
}

type fakeDataDiskSystem struct {
	statInfo       fs.FileInfo
	statErr        error
	findmnt        outputResult
	filesystems    []outputResult
	filesystemCall int
	uuid           outputResult
	runErrors      map[string]error
	runCalls       []commandInvocation
	mkdirErr       error
	readErr        error
	entries        []fs.DirEntry
	appendErr      error
	appendedPath   string
	appendedEntry  string
}

func newFakeDataDiskSystem() *fakeDataDiskSystem {
	return &fakeDataDiskSystem{
		statInfo:    fakeFileInfo{name: "test", mode: fs.ModeDevice},
		findmnt:     outputResult{err: exitStatusError(1)},
		filesystems: []outputResult{{output: []byte("ext4\n")}},
		uuid:        outputResult{output: []byte("disk-uuid\n")},
		runErrors:   make(map[string]error),
	}
}

func (system *fakeDataDiskSystem) ops() dataDiskOps {
	return dataDiskOps{
		stat: func(string) (fs.FileInfo, error) { return system.statInfo, system.statErr },
		output: func(name string, args ...string) ([]byte, error) {
			switch {
			case name == "findmnt":
				return system.findmnt.output, system.findmnt.err
			case name == "blkid" && len(args) > 1 && args[1] == "TYPE":
				index := system.filesystemCall
				system.filesystemCall++
				if index >= len(system.filesystems) {
					return nil, errors.New("unexpected filesystem inspection")
				}
				return system.filesystems[index].output, system.filesystems[index].err
			case name == "blkid" && len(args) > 1 && args[1] == "UUID":
				return system.uuid.output, system.uuid.err
			default:
				return nil, fmt.Errorf("unexpected output command %s %v", name, args)
			}
		},
		run: func(name string, args ...string) error {
			system.runCalls = append(system.runCalls, commandInvocation{name: name, args: append([]string(nil), args...)})
			return system.runErrors[name]
		},
		mkdirAll: func(string, fs.FileMode) error { return system.mkdirErr },
		readDir:  func(string) ([]fs.DirEntry, error) { return system.entries, system.readErr },
		appendFstab: func(path, entry string) error {
			system.appendedPath = path
			system.appendedEntry = entry
			return system.appendErr
		},
	}
}

type exitStatusError int

func (status exitStatusError) Error() string { return fmt.Sprintf("exit status %d", status) }
func (status exitStatusError) ExitCode() int { return int(status) }

type fakeFileInfo struct {
	name string
	mode fs.FileMode
}

func (info fakeFileInfo) Name() string      { return info.name }
func (fakeFileInfo) Size() int64            { return 0 }
func (info fakeFileInfo) Mode() fs.FileMode { return info.mode }
func (fakeFileInfo) ModTime() time.Time     { return time.Time{} }
func (info fakeFileInfo) IsDir() bool       { return info.mode.IsDir() }
func (fakeFileInfo) Sys() any               { return nil }

type fakeDirEntry struct{ name string }

func (entry fakeDirEntry) Name() string               { return entry.name }
func (fakeDirEntry) IsDir() bool                      { return false }
func (fakeDirEntry) Type() fs.FileMode                { return 0 }
func (entry fakeDirEntry) Info() (fs.FileInfo, error) { return fakeFileInfo{name: entry.name}, nil }
