/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package device_connectivity_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

const (
	iscsiTargetA = "iqn.2000-01.com.example:target-a"
	iscsiTargetB = "iqn.2000-01.com.example:target-b"
)

var iscsiCmdTimeoutMsec = int(device_connectivity.IscsiCmdTimeout.Seconds() * 1000)

// =========================================================================
// Helpers: fake host filesystem (RootDir) and fake iscsiadm
// =========================================================================

func iscsiMkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
}

func iscsiWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// iscsiSymlink creates root/rel -> target (target is stored verbatim).
func iscsiSymlink(t *testing.T, target, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

type iscsiSessionSpec struct {
	id        int
	host      int    // > 0: create a real device dir and the session/device symlink pointing into it
	addr      string // empty: do not create an iscsi_connection entry
	port      string
	target    string
	state     string
	initiator string // empty: no session level initiatorname file
}

func iscsiAddSession(t *testing.T, root string, s iscsiSessionSpec) {
	t.Helper()
	sess := fmt.Sprintf("sys/class/iscsi_session/session%d", s.id)
	iscsiWriteFile(t, root, sess+"/state", s.state+"\n")
	iscsiWriteFile(t, root, sess+"/targetname", s.target+"\n")
	if s.initiator != "" {
		iscsiWriteFile(t, root, sess+"/initiatorname", s.initiator+"\n")
	}
	if s.host > 0 {
		realDir := fmt.Sprintf("sys/devices/platform/host%d/session%d", s.host, s.id)
		iscsiMkdir(t, root, realDir)
		iscsiSymlink(t, filepath.Join(root, realDir), root, sess+"/device")
	}
	if s.addr != "" {
		conn := fmt.Sprintf("sys/class/iscsi_connection/connection%d:0", s.id)
		iscsiWriteFile(t, root, conn+"/address", s.addr+"\n")
		iscsiWriteFile(t, root, conn+"/port", s.port+"\n")
		linkHost := s.host
		if linkHost == 0 {
			linkHost = 1
		}
		iscsiSymlink(t, fmt.Sprintf("../../../devices/platform/host%d/session%d/connection%d:0", linkHost, s.id, s.id),
			root, conn+"/device")
	}
}

// fakeExitError lets the GetExitCode stub return a chosen exit code.
type fakeExitError struct{ code int }

func (e *fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func iscsiDiscoverKey(portal string) string {
	return "-m discoverydb -t sendtargets -p " + portal + " --discover --op=update"
}

func iscsiLoginKey(portal, target string) string {
	return "-m node -p " + portal + " -T " + target + " --login"
}

type iscsiFixture struct {
	t      *testing.T
	root   string
	exec   *mocks.MockExecuterInterface
	helper *mocks.MockOsDeviceConnectivityHelperScsiGenericInterface
	iscsi  *device_connectivity.OsDeviceConnectivityIscsi

	mu    sync.Mutex
	calls []string          // iscsiadm argument lines, in call order
	errs  map[string]error  // iscsiadm argument line -> error to return
	outs  map[string]string // iscsiadm argument line -> stdout to return
}

// newIscsiFixture builds an OsDeviceConnectivityIscsi that reads a private temp RootDir and talks
// to a fake iscsiadm (every call is recorded in fx.calls).
func newIscsiFixture(t *testing.T) *iscsiFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	fx := &iscsiFixture{
		t:      t,
		root:   t.TempDir(),
		exec:   mocks.NewMockExecuterInterface(ctrl),
		helper: mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(ctrl),
		errs:   map[string]error{},
		outs:   map[string]string{},
	}
	fx.exec.EXPECT().ExecuteWithTimeout(iscsiCmdTimeoutMsec, "iscsiadm", gomock.Any()).DoAndReturn(
		func(_ int, _ string, args []string) ([]byte, error) {
			key := strings.Join(args, " ")
			fx.mu.Lock()
			defer fx.mu.Unlock()
			fx.calls = append(fx.calls, key)
			return []byte(fx.outs[key]), fx.errs[key]
		}).AnyTimes()
	fx.exec.EXPECT().GetExitCode(gomock.Any()).DoAndReturn(func(err error) (int, bool) {
		var ee *fakeExitError
		if errors.As(err, &ee) {
			return ee.code, true
		}
		return 0, false
	}).AnyTimes()

	fx.iscsi = &device_connectivity.OsDeviceConnectivityIscsi{
		Executer:          fx.exec,
		KeyedGater:        executer.NewKeyedGater(100),
		HelperScsiGeneric: fx.helper,
		RootDir:           fx.root,
	}
	return fx
}

func (fx *iscsiFixture) assertCalls(want ...string) {
	fx.t.Helper()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(want) == 0 && len(fx.calls) == 0 {
		return
	}
	if !reflect.DeepEqual(fx.calls, want) {
		fx.t.Fatalf("unexpected iscsiadm calls:\n got: %q\nwant: %q", fx.calls, want)
	}
}

func (fx *iscsiFixture) countCalls(substr string) int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	n := 0
	for _, c := range fx.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func assertErrContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got %q", substr, err.Error())
	}
}

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// Pure string helpers: ExtractIP, EnsurePort, getDiscoveryScopeKey, normalizePortal
// =========================================================================

func TestIscsiExtractIP(t *testing.T) {
	o := &device_connectivity.OsDeviceConnectivityIscsi{}
	testCases := []struct{ in, want string }{
		{"10.0.0.1:3260", "10.0.0.1"},
		{"10.0.0.1", "10.0.0.1"},
		{"  10.0.0.1:3260  ", "10.0.0.1"},
		{"[2001:DB8::1]:3260", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"[::1]", "::1"},
		{"Host.Example:3260", "host.example"},
	}
	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			if got := o.ExtractIP(tc.in); got != tc.want {
				t.Fatalf("ExtractIP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIscsiEnsurePort(t *testing.T) {
	o := &device_connectivity.OsDeviceConnectivityIscsi{}
	testCases := []struct{ in, want string }{
		{"10.0.0.1", "10.0.0.1:3260"},
		{"  10.0.0.1  ", "10.0.0.1:3260"},
		{"10.0.0.1:3261", "10.0.0.1:3261"},
		{"2001:db8::1", "[2001:db8::1]:3260"},
		{"[2001:db8::1]:3261", "[2001:db8::1]:3261"},
		{"host.example", "host.example:3260"},
	}
	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			if got := o.EnsurePort(tc.in); got != tc.want {
				t.Fatalf("EnsurePort(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIscsiGetDiscoveryScopeKey(t *testing.T) {
	o := &device_connectivity.OsDeviceConnectivityIscsi{}
	testCases := []struct{ name, in, want string }{
		{"ipv4 with port is grouped by /24", "10.1.2.3:3260", "10.1.2"},
		{"ipv4 without port", "10.1.2.200", "10.1.2"},
		{"ipv4 mapped in ipv6 is grouped as ipv4", "::ffff:10.1.2.3", "10.1.2"},
		{"ipv6 is grouped by /64", "[2001:db8:1:2:3::5]:3260", "2001:db8:1:2"},
		{"ipv6 with zero blocks", "[2001:db8::5]:3260", "2001:db8:0:0"},
		{"hostname is returned as is", "Host.Example:3260", "host.example"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := o.DiscoveryScopeKeyForTest(tc.in); got != tc.want {
				t.Fatalf("getDiscoveryScopeKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIscsiNormalizePortal(t *testing.T) {
	o := &device_connectivity.OsDeviceConnectivityIscsi{}
	testCases := []struct{ name, in, want string }{
		{"ipv4 with port", "10.0.0.1:3261", "10.0.0.1:3261"},
		{"ipv4 without port gets default port", " 10.0.0.1 ", "10.0.0.1:3260"},
		{"ipv6 with brackets and port is lowercased", "[2001:DB8::1]:3261", "[2001:db8::1]:3261"},
		{"ipv6 without port gets default port", "2001:DB8::1", "[2001:db8::1]:3260"},
		{"hostname with port is lowercased", "HOST.Example:3260", "host.example:3260"},
		{"hostname without port gets default port", "Host.Example", "host.example:3260"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := o.NormalizePortalForTest(tc.in); got != tc.want {
				t.Fatalf("normalizePortal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIscsiCleanSysfsData(t *testing.T) {
	testCases := []struct {
		in   string
		want string
	}{
		{"LOGGED_IN\n", "LOGGED_IN"},
		{"  \tvalue\r\n\x00", "value"},
		{"", ""},
		{"a b", "a b"},
	}
	for _, tc := range testCases {
		if got := device_connectivity.CleanSysfsDataForTest([]byte(tc.in)); got != tc.want {
			t.Fatalf("cleanSysfsData(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIscsiReadSysfs(t *testing.T) {
	fx := newIscsiFixture(t)
	iscsiWriteFile(t, fx.root, "file", " LOGGED_IN\n\x00")

	got, err := fx.iscsi.ReadSysfsForTest(filepath.Join(fx.root, "file"))
	assertNoErr(t, err)
	if got != "LOGGED_IN" {
		t.Fatalf("readSysfs = %q, want LOGGED_IN", got)
	}

	if _, err = fx.iscsi.ReadSysfsForTest(filepath.Join(fx.root, "missing")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// =========================================================================
// iscsiadm wrappers: iscsiCmd, iscsiDiscover, iscsiLogin
// =========================================================================

func TestIscsiCmd(t *testing.T) {
	fx := newIscsiFixture(t)
	fx.outs["-m session"] = "some output"
	out, err := fx.iscsi.IscsiCmdForTest("-m", "session")
	assertNoErr(t, err)
	if out != "some output" {
		t.Fatalf("iscsiCmd output = %q", out)
	}

	fx.errs["-m node"] = errors.New("boom")
	if _, err = fx.iscsi.IscsiCmdForTest("-m", "node"); err == nil {
		t.Fatal("expected the executer error to be returned")
	}
	fx.assertCalls("-m session", "-m node")
}

func TestIscsiDiscover(t *testing.T) {
	t.Run("adds the default port and succeeds", func(t *testing.T) {
		fx := newIscsiFixture(t)
		assertNoErr(t, fx.iscsi.IscsiDiscoverForTest(context.Background(), "10.0.0.1"))
		fx.assertCalls(iscsiDiscoverKey("10.0.0.1:3260"))
	})
	t.Run("keeps an explicit port", func(t *testing.T) {
		fx := newIscsiFixture(t)
		assertNoErr(t, fx.iscsi.IscsiDiscoverForTest(context.Background(), "10.0.0.1:3261"))
		fx.assertCalls(iscsiDiscoverKey("10.0.0.1:3261"))
	})
	t.Run("returns the command failure with output", func(t *testing.T) {
		fx := newIscsiFixture(t)
		key := iscsiDiscoverKey("10.0.0.1:3260")
		fx.errs[key] = errors.New("discover failed")
		fx.outs[key] = " no route to host \n"
		err := fx.iscsi.IscsiDiscoverForTest(context.Background(), "10.0.0.1")
		assertErrContains(t, err, "iscsiadm discovery failed for portal 10.0.0.1:3260")
		assertErrContains(t, err, "no route to host")
		assertErrContains(t, err, "discover failed")
	})
}

func TestIscsiLogin(t *testing.T) {
	portal := "10.0.0.1:3260"
	key := iscsiLoginKey(portal, iscsiTargetA)

	testCases := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "success"},
		{name: "exit code 15 (already logged in) is success", err: &fakeExitError{code: 15}},
		{name: "exit code 24 (session exists) is success", err: &fakeExitError{code: 24}},
		{name: "other exit code fails", err: &fakeExitError{code: 8}, wantErr: true},
		{name: "non exit error fails", err: errors.New("cannot exec"), wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newIscsiFixture(t)
			fx.errs[key] = tc.err
			fx.outs[key] = "login output"
			err := fx.iscsi.IscsiLoginForTest(context.Background(), iscsiTargetA, "10.0.0.1")
			if tc.wantErr {
				assertErrContains(t, err, "iscsiadm login failed")
				assertErrContains(t, err, "login output")
			} else {
				assertNoErr(t, err)
			}
			fx.assertCalls(key)
		})
	}
}

// =========================================================================
// Active sessions from sysfs: iscsiGetRawSessions, getAllSessions, parseRawSessions, filterLoggedIn
// =========================================================================

func TestIscsiGetRawSessions(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.IscsiGetRawSessionsForTest(cancelledCtx())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("missing connection class directory means no sessions", func(t *testing.T) {
		fx := newIscsiFixture(t)
		lines, err := fx.iscsi.IscsiGetRawSessionsForTest(context.Background())
		assertNoErr(t, err)
		if len(lines) != 0 {
			t.Fatalf("expected no sessions, got %q", lines)
		}
	})

	t.Run("connection class path that cannot be opened is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sys/class", "i am a file, not a directory")
		_, err := fx.iscsi.IscsiGetRawSessionsForTest(context.Background())
		assertErrContains(t, err, "failed to read")
	})

	t.Run("connection class path that cannot be listed is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection", "i am a file, not a directory")
		_, err := fx.iscsi.IscsiGetRawSessionsForTest(context.Background())
		assertErrContains(t, err, "failed to parse iscsi connections stream")
	})

	t.Run("only logged in sessions with a complete connection are reported", func(t *testing.T) {
		fx := newIscsiFixture(t)
		// valid
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
			target: iscsiTargetA, state: "LOGGED_IN"})
		// valid, IPv6 portal
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 2, host: 5, addr: "2001:db8::1", port: "3261",
			target: iscsiTargetB, state: "LOGGED_IN"})
		// not logged in
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 3, host: 6, addr: "10.0.0.3", port: "3260",
			target: iscsiTargetA, state: "FAILED"})
		// connection entry without a port file
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection4:0/address", "10.0.0.4\n")
		// entry that is not a connection
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/something_else/address", "10.0.0.5\n")
		// connection without a device link that is not a symlink itself
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection6:0/address", "10.0.0.6\n")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection6:0/port", "3260\n")
		// connection whose session is missing in sysfs (link has no sessionX component -> session0)
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection7:0/address", "10.0.0.7\n")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection7:0/port", "3260\n")
		iscsiSymlink(t, "../../../devices/nowhere", fx.root, "sys/class/iscsi_connection/connection7:0/device")
		// connection whose session lacks a targetname
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection8:0/address", "10.0.0.8\n")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_connection/connection8:0/port", "3260\n")
		iscsiSymlink(t, "../../../devices/platform/host8/session8/connection8:0", fx.root,
			"sys/class/iscsi_connection/connection8:0/device")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_session/session8/state", "LOGGED_IN\n")

		lines, err := fx.iscsi.IscsiGetRawSessionsForTest(context.Background())
		assertNoErr(t, err)
		sort.Strings(lines)
		want := []string{
			"tcp: [1] 10.0.0.1:3260 " + iscsiTargetA,
			"tcp: [2] [2001:db8::1]:3261 " + iscsiTargetB,
		}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("got %q, want %q", lines, want)
		}
	})

	t.Run("connection that is itself a symlink to the session tree", func(t *testing.T) {
		fx := newIscsiFixture(t)
		realConn := "sys/devices/platform/host9/session9/connection9:0"
		iscsiWriteFile(t, fx.root, realConn+"/address", "10.0.0.9\n")
		iscsiWriteFile(t, fx.root, realConn+"/port", "3260\n")
		iscsiSymlink(t, filepath.Join(fx.root, realConn), fx.root, "sys/class/iscsi_connection/connection9:0")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_session/session9/state", "LOGGED_IN\n")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_session/session9/targetname", iscsiTargetA+"\n")

		lines, err := fx.iscsi.IscsiGetRawSessionsForTest(context.Background())
		assertNoErr(t, err)
		want := []string{"tcp: [9] 10.0.0.9:3260 " + iscsiTargetA}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("got %q, want %q", lines, want)
		}
	})
}

func TestIscsiParseRawSessions(t *testing.T) {
	o := &device_connectivity.OsDeviceConnectivityIscsi{}

	t.Run("valid lines are grouped by lowercased target and portal ip", func(t *testing.T) {
		got, err := o.ParseRawSessionsForTest([]string{
			"",
			"   ",
			"udp: [3] 10.0.0.9:3260 iqn.ignored",
			"tcp: [1] 10.0.0.1:3260 IQN.Target-A",
			"tcp: [2] [2001:db8::1]:3260 iqn.target-a",
			"tcp: [4] 10.0.0.2:3260 iqn.target-b",
		})
		assertNoErr(t, err)
		want := map[string]map[string]bool{
			"iqn.target-a": {"10.0.0.1": true, "2001:db8::1": true},
			"iqn.target-b": {"10.0.0.2": true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("no lines", func(t *testing.T) {
		got, err := o.ParseRawSessionsForTest(nil)
		assertNoErr(t, err)
		if len(got) != 0 {
			t.Fatalf("expected an empty map, got %v", got)
		}
	})

	t.Run("truncated line is an error", func(t *testing.T) {
		_, err := o.ParseRawSessionsForTest([]string{"tcp: [1] 10.0.0.1:3260"})
		assertErrContains(t, err, "truncation detected")
	})
}

func TestIscsiGetAllSessions(t *testing.T) {
	t.Run("sysfs error is returned", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.GetAllSessionsForTest(cancelledCtx())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("sessions are parsed", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
			target: strings.ToUpper(iscsiTargetA), state: "LOGGED_IN"})
		got, err := fx.iscsi.GetAllSessionsForTest(context.Background())
		assertNoErr(t, err)
		want := map[string]map[string]bool{iscsiTargetA: {"10.0.0.1": true}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a session with an empty target name makes the line unparsable", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
			target: "", state: "LOGGED_IN"})
		_, err := fx.iscsi.GetAllSessionsForTest(context.Background())
		assertErrContains(t, err, "truncation detected")
	})
}

func TestIscsiFilterLoggedIn(t *testing.T) {
	t.Run("sysfs error is returned", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.FilterLoggedInForTest(cancelledCtx(), map[string][]string{iscsiTargetA: {"10.0.0.1"}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("only missing portals are kept", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
			target: iscsiTargetA, state: "LOGGED_IN"})

		got, err := fx.iscsi.FilterLoggedInForTest(context.Background(), map[string][]string{
			strings.ToUpper(iscsiTargetA): {"10.0.0.1:3260", "10.0.0.2:3260"}, // target name case is ignored
			iscsiTargetB:                  {"10.0.0.1"},                       // other target, same ip
		})
		assertNoErr(t, err)
		want := map[string][]string{
			strings.ToUpper(iscsiTargetA): {"10.0.0.2:3260"},
			iscsiTargetB:                  {"10.0.0.1"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("everything is logged in", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
			target: iscsiTargetA, state: "LOGGED_IN"})
		got, err := fx.iscsi.FilterLoggedInForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260"}})
		assertNoErr(t, err)
		if len(got) != 0 {
			t.Fatalf("expected nothing to log in, got %v", got)
		}
	})
}

// =========================================================================
// Local iscsi node database: loadRelevantTargets
// =========================================================================

func TestIscsiLoadRelevantTargets(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.LoadRelevantTargetsForTest(cancelledCtx(), map[string][]string{iscsiTargetA: nil})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("missing database yields empty entries for every requested target", func(t *testing.T) {
		fx := newIscsiFixture(t)
		got, err := fx.iscsi.LoadRelevantTargetsForTest(context.Background(),
			map[string][]string{strings.ToUpper(iscsiTargetA): nil})
		assertNoErr(t, err)
		want := map[string]map[string]bool{iscsiTargetA: {}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("database path that cannot be listed is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "etc/iscsi/nodes", "i am a file, not a directory")
		_, err := fx.iscsi.LoadRelevantTargetsForTest(context.Background(), map[string][]string{iscsiTargetA: nil})
		assertErrContains(t, err, "failed to parse local iscsi database stream")
	})

	t.Run("portal directories are indexed by ip", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodes := "etc/iscsi/nodes"
		iscsiMkdir(t, fx.root, nodes+"/"+iscsiTargetA+"/10.0.0.1,3260,1")
		iscsiMkdir(t, fx.root, nodes+"/"+iscsiTargetA+"/2001:db8::1,3260,2")
		iscsiMkdir(t, fx.root, nodes+"/"+iscsiTargetA+"/no-commas-here")  // ignored: not "ip,port,tpgt"
		iscsiMkdir(t, fx.root, nodes+"/iqn.2000-01.com.example:unrequested/10.9.9.9,3260,1") // not requested
		iscsiWriteFile(t, fx.root, nodes+"/iqn.file", "target entry is a file")
		iscsiSymlink(t, "/this/does/not/exist", fx.root, nodes+"/iqn.broken")

		got, err := fx.iscsi.LoadRelevantTargetsForTest(context.Background(), map[string][]string{
			strings.ToUpper(iscsiTargetA): nil,
			"iqn.nodir":                   nil,
			"iqn.file":                    nil,
			"iqn.broken":                  nil,
		})
		assertNoErr(t, err)
		want := map[string]map[string]bool{
			iscsiTargetA: {"10.0.0.1": true, "2001:db8::1": true},
			"iqn.nodir":  {},
			"iqn.file":   {},
			"iqn.broken": {},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

// =========================================================================
// Discovery + login: discoverAndLogin
// =========================================================================

func TestIscsiDiscoverAndLogin(t *testing.T) {
	nodeDB := func(t *testing.T, fx *iscsiFixture, target, portalDir string) {
		iscsiMkdir(t, fx.root, "etc/iscsi/nodes/"+target+"/"+portalDir)
	}

	t.Run("cancelled context", func(t *testing.T) {
		fx := newIscsiFixture(t)
		err := fx.iscsi.DiscoverAndLoginForTest(cancelledCtx(), map[string][]string{iscsiTargetA: {"10.0.0.1"}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		fx.assertCalls()
	})

	t.Run("portal already in the node database is logged in without discovery", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodeDB(t, fx, iscsiTargetA, "10.0.0.1,3260,1")
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260"}}))
		fx.assertCalls(iscsiLoginKey("10.0.0.1:3260", iscsiTargetA))
	})

	t.Run("portal missing from the database is discovered and then logged in", func(t *testing.T) {
		fx := newIscsiFixture(t)
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.2"}}))
		fx.assertCalls(
			iscsiDiscoverKey("10.0.0.2:3260"),
			iscsiLoginKey("10.0.0.2:3260", iscsiTargetA),
		)
	})

	t.Run("failed discovery of the only portal is an error and nothing is logged in", func(t *testing.T) {
		fx := newIscsiFixture(t)
		fx.errs[iscsiDiscoverKey("10.0.0.2:3260")] = errors.New("discover failed")
		err := fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.2"}})
		assertErrContains(t, err, "no targets were successfully discovered")
		fx.assertCalls(iscsiDiscoverKey("10.0.0.2:3260"))
	})

	t.Run("only portals that were discovered or cached are logged in", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodeDB(t, fx, iscsiTargetA, "10.0.0.1,3260,1")
		fx.errs[iscsiDiscoverKey("10.0.0.2:3260")] = errors.New("discover failed")
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260", "10.0.0.2:3260", "10.0.0.3:3260"}}))
		fx.assertCalls(
			iscsiDiscoverKey("10.0.0.2:3260"),
			iscsiDiscoverKey("10.0.0.3:3260"),
			iscsiLoginKey("10.0.0.1:3260", iscsiTargetA),
			iscsiLoginKey("10.0.0.3:3260", iscsiTargetA),
		)
	})

	t.Run("a portal shared by two targets is discovered once", func(t *testing.T) {
		fx := newIscsiFixture(t)
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(), map[string][]string{
			iscsiTargetA: {"10.0.0.5:3260"},
			iscsiTargetB: {"10.0.0.5:3260"},
		}))
		if n := fx.countCalls("discoverydb"); n != 1 {
			t.Fatalf("expected a single discovery, got %d (%q)", n, fx.calls)
		}
		if n := fx.countCalls("--login"); n < 1 {
			t.Fatalf("expected at least one login, got %d (%q)", n, fx.calls)
		}
	})

	t.Run("already logged in / session exists exit codes are not failures", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodeDB(t, fx, iscsiTargetA, "10.0.0.1,3260,1")
		nodeDB(t, fx, iscsiTargetA, "10.0.0.2,3260,1")
		fx.errs[iscsiLoginKey("10.0.0.1:3260", iscsiTargetA)] = &fakeExitError{code: 15}
		fx.errs[iscsiLoginKey("10.0.0.2:3260", iscsiTargetA)] = &fakeExitError{code: 24}
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260", "10.0.0.2:3260"}}))
	})

	t.Run("some logins failing is tolerated", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodeDB(t, fx, iscsiTargetA, "10.0.0.1,3260,1")
		nodeDB(t, fx, iscsiTargetA, "10.0.0.2,3260,1")
		fx.errs[iscsiLoginKey("10.0.0.1:3260", iscsiTargetA)] = &fakeExitError{code: 8}
		assertNoErr(t, fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260", "10.0.0.2:3260"}}))
	})

	t.Run("all logins failing is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		nodeDB(t, fx, iscsiTargetA, "10.0.0.1,3260,1")
		nodeDB(t, fx, iscsiTargetA, "10.0.0.2,3260,1")
		fx.errs[iscsiLoginKey("10.0.0.1:3260", iscsiTargetA)] = &fakeExitError{code: 8}
		fx.errs[iscsiLoginKey("10.0.0.2:3260", iscsiTargetA)] = errors.New("cannot exec")
		err := fx.iscsi.DiscoverAndLoginForTest(context.Background(),
			map[string][]string{iscsiTargetA: {"10.0.0.1:3260", "10.0.0.2:3260"}})
		assertErrContains(t, err, "all iSCSI login attempts failed")
	})
}

// =========================================================================
// EnsureLogin (public entry point)
// =========================================================================

func TestEnsureLogin(t *testing.T) {
	portal1, portal2 := "10.0.0.1:3260", "10.0.0.2:3260"
	portalsByTarget := map[string][]string{iscsiTargetA: {portal1, portal2}}
	loggedIn := func(id int, addr string) iscsiSessionSpec {
		return iscsiSessionSpec{id: id, host: id + 3, addr: addr, port: "3260", target: iscsiTargetA, state: "LOGGED_IN"}
	}

	testCases := []struct {
		name  string
		setup func(t *testing.T, fx *iscsiFixture)
		ctx   context.Context
		want  []string
	}{
		{
			name: "Should not login when the sessions cannot be read",
			ctx:  cancelledCtx(),
		},
		{
			name: "Should not login when the session list is corrupt",
			setup: func(t *testing.T, fx *iscsiFixture) {
				iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, addr: "10.0.0.1", port: "3260",
					target: "", state: "LOGGED_IN"})
			},
		},
		{
			name: "Should not login when all relevant portals are already logged in",
			setup: func(t *testing.T, fx *iscsiFixture) {
				iscsiAddSession(t, fx.root, loggedIn(1, "10.0.0.1"))
				iscsiAddSession(t, fx.root, loggedIn(2, "10.0.0.2"))
			},
		},
		{
			name: "Should not login when the only discover call fails",
			setup: func(t *testing.T, fx *iscsiFixture) {
				iscsiAddSession(t, fx.root, loggedIn(1, "10.0.0.1"))
				fx.errs[iscsiDiscoverKey(portal2)] = errors.New("discover failed")
			},
			want: []string{iscsiDiscoverKey(portal2)},
		},
		{
			name: "Should login only the missing portal when it is in the node database",
			setup: func(t *testing.T, fx *iscsiFixture) {
				iscsiAddSession(t, fx.root, loggedIn(1, "10.0.0.1"))
				iscsiMkdir(t, fx.root, "etc/iscsi/nodes/"+iscsiTargetA+"/10.0.0.2,3260,1")
			},
			want: []string{iscsiLoginKey(portal2, iscsiTargetA)},
		},
		{
			name: "Should login only the portals whose discover call succeeds",
			setup: func(t *testing.T, fx *iscsiFixture) {
				fx.errs[iscsiDiscoverKey(portal1)] = errors.New("discover failed")
			},
			want: []string{
				iscsiDiscoverKey(portal1),
				iscsiDiscoverKey(portal2),
				iscsiLoginKey(portal2, iscsiTargetA),
			},
		},
		{
			name: "Should not panic when every login fails",
			setup: func(t *testing.T, fx *iscsiFixture) {
				fx.errs[iscsiLoginKey(portal1, iscsiTargetA)] = &fakeExitError{code: 8}
				fx.errs[iscsiLoginKey(portal2, iscsiTargetA)] = &fakeExitError{code: 8}
			},
			want: []string{
				iscsiDiscoverKey(portal1),
				iscsiDiscoverKey(portal2),
				iscsiLoginKey(portal1, iscsiTargetA),
				iscsiLoginKey(portal2, iscsiTargetA),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newIscsiFixture(t)
			if tc.setup != nil {
				tc.setup(t, fx)
			}
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			fx.iscsi.EnsureLogin(ctx, portalsByTarget)
			fx.assertCalls(tc.want...)
		})
	}
}

// =========================================================================
// Initiator discovery: extractHostFromDeviceLink, parseActiveSessions, getInitiatorIQN, updateHostIDs
// =========================================================================

func TestIscsiExtractHostFromDeviceLink(t *testing.T) {
	t.Run("host number is taken from the resolved path", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "sys/devices/platform/host12/session3")
		iscsiSymlink(t, filepath.Join(fx.root, "sys/devices/platform/host12/session3"), fx.root, "sess/device")
		got, err := fx.iscsi.ExtractHostFromDeviceLinkForTest(filepath.Join(fx.root, "sess"))
		assertNoErr(t, err)
		if got != 12 {
			t.Fatalf("got host %d, want 12", got)
		}
	})

	t.Run("unresolvable link", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.ExtractHostFromDeviceLinkForTest(filepath.Join(fx.root, "sess"))
		assertErrContains(t, err, "failed to resolve device link")
	})

	t.Run("non numeric host component and no other host component", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "sys/devices/hostX/session3")
		iscsiSymlink(t, filepath.Join(fx.root, "sys/devices/hostX/session3"), fx.root, "sess/device")
		_, err := fx.iscsi.ExtractHostFromDeviceLinkForTest(filepath.Join(fx.root, "sess"))
		assertErrContains(t, err, "could not find host identifier")
	})
}

func TestIscsiGetInitiatorIQN(t *testing.T) {
	t.Run("session level initiatorname wins", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sess/initiatorname", " iqn.session \n")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_host/host4/initiatorname", "iqn.host\n")
		got, err := fx.iscsi.GetInitiatorIQNForTest(filepath.Join(fx.root, "sess"), "host4")
		assertNoErr(t, err)
		if got != "iqn.session" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("host level initiatorname is the first fallback", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_host/host4/initiatorname", "iqn.host\n")
		iscsiWriteFile(t, fx.root, "etc/iscsi/initiatorname.iscsi", "InitiatorName=iqn.global\n")
		got, err := fx.iscsi.GetInitiatorIQNForTest(filepath.Join(fx.root, "sess"), "host4")
		assertNoErr(t, err)
		if got != "iqn.host" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("global config is the last fallback", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "etc/iscsi/initiatorname.iscsi", strings.Join([]string{
			"## comment",
			"",
			"   # indented comment",
			"not a key value line",
			"Other = 1",
			"initiatorname = iqn.global ",
		}, "\n"))
		got, err := fx.iscsi.GetInitiatorIQNForTest(filepath.Join(fx.root, "sess"), "host4")
		assertNoErr(t, err)
		if got != "iqn.global" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("global config without an InitiatorName key", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "etc/iscsi/initiatorname.iscsi", "# nothing\nOther=1\n")
		_, err := fx.iscsi.GetInitiatorIQNForTest(filepath.Join(fx.root, "sess"), "host4")
		assertErrContains(t, err, "initiator IQN not found")
	})

	t.Run("no source at all", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.GetInitiatorIQNForTest(filepath.Join(fx.root, "sess"), "host4")
		assertErrContains(t, err, "initiator IQN not found")
	})

	t.Run("empty RootDir reads the real host paths without failing", func(t *testing.T) {
		o := &device_connectivity.OsDeviceConnectivityIscsi{}
		// The result depends on the machine running the test; this only exercises the unmapped path.
		_, _ = o.GetInitiatorIQNForTest("/nonexistent-session-dir", "host-does-not-exist")
	})
}

func TestIscsiParseActiveSessions(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.ParseActiveSessionsForTest(cancelledCtx())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("missing sysfs class means no sessions", func(t *testing.T) {
		fx := newIscsiFixture(t)
		got, err := fx.iscsi.ParseActiveSessionsForTest(context.Background())
		assertNoErr(t, err)
		if len(got) != 0 {
			t.Fatalf("expected no sessions, got %v", got)
		}
	})

	t.Run("sysfs class that cannot be opened is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sys/class", "i am a file")
		_, err := fx.iscsi.ParseActiveSessionsForTest(context.Background())
		assertErrContains(t, err, "failed to read iSCSI sessions from sysfs")
	})

	t.Run("sysfs class that cannot be listed is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_session", "i am a file")
		_, err := fx.iscsi.ParseActiveSessionsForTest(context.Background())
		assertErrContains(t, err, "failed to parse active iscsi sessions stream")
	})

	t.Run("only usable logged in sessions are returned", func(t *testing.T) {
		fx := newIscsiFixture(t)
		// session level initiator
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init-1"})
		// host level initiator
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 2, host: 5, target: iscsiTargetA, state: "LOGGED_IN"})
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_host/host5/initiatorname", "iqn.init-2\n")
		// not logged in
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 3, host: 6, target: iscsiTargetA,
			state: "FAILED", initiator: "iqn.init-3"})
		// no state file at all
		iscsiMkdir(t, fx.root, "sys/class/iscsi_session/session4")
		// logged in but no device link
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 5, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init-5"})
		// logged in, host resolved, but no initiator name anywhere
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 6, host: 7, target: iscsiTargetA, state: "LOGGED_IN"})

		got, err := fx.iscsi.ParseActiveSessionsForTest(context.Background())
		assertNoErr(t, err)
		sort.Slice(got, func(i, j int) bool { return got[i].HostNum < got[j].HostNum })
		want := []device_connectivity.ActiveSessionForTest{
			{SourceIQN: "iqn.init-1", HostNum: 4},
			{SourceIQN: "iqn.init-2", HostNum: 5},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestIscsiUpdateHostIDs(t *testing.T) {
	t.Run("hosts sharing an initiator with a known host are added", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init:A"})
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 2, host: 5, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "IQN.INIT:a"}) // same initiator, different case
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 3, host: 6, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init:b"}) // different initiator
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 4, host: 7, target: iscsiTargetA,
			state: "FAILED", initiator: "iqn.init:A"}) // not logged in

		hostIDs := map[int]bool{4: true}
		fx.iscsi.UpdateHostIDsForTest(context.Background(), hostIDs)
		want := map[int]bool{4: true, 5: true}
		if !reflect.DeepEqual(hostIDs, want) {
			t.Fatalf("got %v, want %v", hostIDs, want)
		}
	})

	t.Run("unknown hosts do not pull in anything", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init:A"})
		hostIDs := map[int]bool{9: true}
		fx.iscsi.UpdateHostIDsForTest(context.Background(), hostIDs)
		if !reflect.DeepEqual(hostIDs, map[int]bool{9: true}) {
			t.Fatalf("host ids changed: %v", hostIDs)
		}
	})

	t.Run("no active sessions", func(t *testing.T) {
		fx := newIscsiFixture(t)
		hostIDs := map[int]bool{4: true}
		fx.iscsi.UpdateHostIDsForTest(context.Background(), hostIDs)
		if !reflect.DeepEqual(hostIDs, map[int]bool{4: true}) {
			t.Fatalf("host ids changed: %v", hostIDs)
		}
	})

	t.Run("session parsing failure leaves the host ids untouched", func(t *testing.T) {
		fx := newIscsiFixture(t)
		hostIDs := map[int]bool{4: true}
		fx.iscsi.UpdateHostIDsForTest(cancelledCtx(), hostIDs)
		if !reflect.DeepEqual(hostIDs, map[int]bool{4: true}) {
			t.Fatalf("host ids changed: %v", hostIDs)
		}
	})
}

// =========================================================================
// RescanDevices and the plain delegations to the SCSI helper
// =========================================================================

func TestIscsiRescanDevices(t *testing.T) {
	ctx := context.Background()
	arrayIds := []string{"array-id"}

	t.Run("host id lookup failure is returned", func(t *testing.T) {
		fx := newIscsiFixture(t)
		fx.helper.EXPECT().RescanDevicesGetHostIds(3, arrayIds).Return(nil, errors.New("lookup failed"))
		assertErrContains(t, fx.iscsi.RescanDevices(ctx, 3, arrayIds), "lookup failed")
	})

	t.Run("host ids are extended with hosts of the same initiator before the rescan", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 1, host: 4, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init:A"})
		iscsiAddSession(t, fx.root, iscsiSessionSpec{id: 2, host: 5, target: iscsiTargetA,
			state: "LOGGED_IN", initiator: "iqn.init:A"})
		fx.helper.EXPECT().RescanDevicesGetHostIds(3, arrayIds).Return(map[int]bool{4: true}, nil)
		fx.helper.EXPECT().RescanDevices(ctx, 3, arrayIds, map[int]bool{4: true, 5: true}).Return(nil)
		assertNoErr(t, fx.iscsi.RescanDevices(ctx, 3, arrayIds))
	})

	t.Run("rescan failure is returned", func(t *testing.T) {
		fx := newIscsiFixture(t)
		fx.helper.EXPECT().RescanDevicesGetHostIds(3, arrayIds).Return(map[int]bool{4: true}, nil)
		fx.helper.EXPECT().RescanDevices(ctx, 3, arrayIds, map[int]bool{4: true}).Return(errors.New("rescan failed"))
		assertErrContains(t, fx.iscsi.RescanDevices(ctx, 3, arrayIds), "rescan failed")
	})
}

func TestIscsiDelegationToScsiHelper(t *testing.T) {
	ctx := context.Background()

	t.Run("GetMpathDevice", func(t *testing.T) {
		fx := newIscsiFixture(t)
		fx.helper.EXPECT().GetMpathDevice(ctx, "vol").Return("dm-3", nil)
		got, err := fx.iscsi.GetMpathDevice(ctx, "vol")
		assertNoErr(t, err)
		if got != "dm-3" {
			t.Fatalf("got %q", got)
		}

		fx.helper.EXPECT().GetMpathDevice(ctx, "bad").Return("", errors.New("no mpath"))
		_, err = fx.iscsi.GetMpathDevice(ctx, "bad")
		assertErrContains(t, err, "no mpath")
	})

	t.Run("RemovePhysicalDevice", func(t *testing.T) {
		fx := newIscsiFixture(t)
		devs := []string{"sda", "sdb"}
		fx.helper.EXPECT().RemovePhysicalDevice(ctx, devs).Return(nil)
		assertNoErr(t, fx.iscsi.RemovePhysicalDevice(ctx, devs))

		fx.helper.EXPECT().RemovePhysicalDevice(ctx, devs).Return(errors.New("remove failed"))
		assertErrContains(t, fx.iscsi.RemovePhysicalDevice(ctx, devs), "remove failed")
	})

	t.Run("RemoveGhostDevice", func(t *testing.T) {
		fx := newIscsiFixture(t)
		ids := []string{"array-id"}
		fx.helper.EXPECT().RemoveGhostDevice(ctx, "serial", 2, ids).Return(nil)
		assertNoErr(t, fx.iscsi.RemoveGhostDevice(ctx, "serial", 2, ids))

		fx.helper.EXPECT().RemoveGhostDevice(ctx, "serial", 2, ids).Return(errors.New("ghost failed"))
		assertErrContains(t, fx.iscsi.RemoveGhostDevice(ctx, "serial", 2, ids), "ghost failed")
	})

	t.Run("ValidateLun", func(t *testing.T) {
		fx := newIscsiFixture(t)
		devs := []string{"sda"}
		fx.helper.EXPECT().ValidateLun(ctx, "dm-3", 2, devs, "serial").Return(nil)
		assertNoErr(t, fx.iscsi.ValidateLun(ctx, "dm-3", 2, devs, "serial"))

		fx.helper.EXPECT().ValidateLun(ctx, "dm-3", 2, devs, "serial").Return(errors.New("lun mismatch"))
		assertErrContains(t, fx.iscsi.ValidateLun(ctx, "dm-3", 2, devs, "serial"), "lun mismatch")
	})
}

// =========================================================================
// GetBlockDeviceForSession
// =========================================================================

func TestIscsiGetBlockDeviceForSession(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.GetBlockDeviceForSession(cancelledCtx(), "1")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("missing /dev is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "1")
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("/dev that cannot be listed is an error", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiWriteFile(t, fx.root, "dev", "i am a file")
		_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "1")
		assertErrContains(t, err, "failed to parse dev stream")
	})

	t.Run("disk is found through /sys/block/<disk>/device", func(t *testing.T) {
		fx := newIscsiFixture(t)
		for _, name := range []string{"sda", "sda1", "sdb", "sd", "sdc", "loop0"} {
			iscsiWriteFile(t, fx.root, "dev/"+name, "")
		}
		for name, session := range map[string]string{"sda": "session1", "sdb": "session2"} {
			realDev := fmt.Sprintf("sys/devices/platform/host4/%s/target4:0:0/4:0:0:1", session)
			iscsiMkdir(t, fx.root, realDev)
			iscsiSymlink(t, filepath.Join(fx.root, realDev), fx.root, "sys/block/"+name+"/device")
		}
		// sdc has no /sys/block entry; sda1 / sd / loop0 must never be considered

		got, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "2")
		assertNoErr(t, err)
		if got != "/dev/sdb" {
			t.Fatalf("got %q, want /dev/sdb", got)
		}
		got, err = fx.iscsi.GetBlockDeviceForSession(context.Background(), "1")
		assertNoErr(t, err)
		if got != "/dev/sda" {
			t.Fatalf("got %q, want /dev/sda", got)
		}
		// session1 must not match session11
		_, err = fx.iscsi.GetBlockDeviceForSession(context.Background(), "11")
		if err == nil {
			t.Fatal("expected an error for a session without disks")
		}
	})

	t.Run("fallback: session device tree", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "dev")
		dev := "sys/class/iscsi_session/session7/device"
		iscsiMkdir(t, fx.root, dev+"/target7:0:0/7:0:0:1/block/sdz")
		iscsiMkdir(t, fx.root, dev+"/target7:0:0/7:0:0:2")       // lun without block dir
		iscsiMkdir(t, fx.root, dev+"/target7:0:0/7:0:0:3/block") // lun with empty block dir
		iscsiWriteFile(t, fx.root, dev+"/target8-file", "x")      // target entry that is a file
		iscsiSymlink(t, "/this/does/not/exist", fx.root, dev+"/target9-broken")
		iscsiWriteFile(t, fx.root, dev+"/uevent", "not a target")

		got, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "7")
		assertNoErr(t, err)
		if got != "/dev/sdz" {
			t.Fatalf("got %q, want /dev/sdz", got)
		}
	})

	t.Run("fallback: session without a matching disk", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "dev")
		iscsiMkdir(t, fx.root, "sys/class/iscsi_session/session6/device/target6:0:0/6:0:0:1")
		_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "6")
		assertErrContains(t, err, "no block device found for session 6")
	})

	t.Run("fallback: missing session device tree", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "dev")
		_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "5")
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("fallback: session device tree that cannot be listed", func(t *testing.T) {
		fx := newIscsiFixture(t)
		iscsiMkdir(t, fx.root, "dev")
		iscsiWriteFile(t, fx.root, "sys/class/iscsi_session/session8/device", "i am a file")
		_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), "8")
		assertErrContains(t, err, "failed to parse fallback session tree")
	})

	t.Run("scans are capped at 10000 entries per level", func(t *testing.T) {
		if testing.Short() {
			t.Skip("creates ~30000 files")
		}
		const over = 10001
		fx := newIscsiFixture(t)

		// /dev level
		for i := 0; i < over; i++ {
			iscsiWriteFile(t, fx.root, fmt.Sprintf("dev/entry%d", i), "")
		}
		// session device (target) level
		dev := "sys/class/iscsi_session/session4/device"
		for i := 0; i < over; i++ {
			iscsiWriteFile(t, fx.root, fmt.Sprintf("%s/target%d", dev, i), "")
		}
		// lun level
		devLuns := "sys/class/iscsi_session/session3/device/target3:0:0"
		for i := 0; i < over; i++ {
			iscsiWriteFile(t, fx.root, fmt.Sprintf("%s/lun%d", devLuns, i), "")
		}

		for _, id := range []string{"4", "3"} {
			_, err := fx.iscsi.GetBlockDeviceForSession(context.Background(), id)
			assertErrContains(t, err, "no block device found for session "+id)
		}
	})
}

// =========================================================================
// Constructor
// =========================================================================

func TestNewOsDeviceConnectivityIscsi(t *testing.T) {
	ctrl := gomock.NewController(t)
	fakeExecuter := mocks.NewMockExecuterInterface(ctrl)
	gater := executer.NewKeyedGater(100)

	got := device_connectivity.NewOsDeviceConnectivityIscsi(fakeExecuter, gater, nil, true)
	o, ok := got.(*device_connectivity.OsDeviceConnectivityIscsi)
	if !ok {
		t.Fatalf("unexpected type %T", got)
	}
	if o.Executer != fakeExecuter || o.KeyedGater != gater || o.HelperScsiGeneric == nil || o.RootDir != "" {
		t.Fatalf("constructor did not wire the dependencies: %+v", o)
	}
}
