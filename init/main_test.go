package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	errMockDial  = errors.New("simulated dial failure")
	errMockOpen  = errors.New("simulated open failure")
	errMockWrite = errors.New("simulated write failure")
	errMockClose = errors.New("simulated close failure")
	errMockScrub = errors.New("simulated scrub failure")
)

var (
	mockProcDirectory    string
	mockDialAttemptCount int
	mockDialerInstance   = &mockSocketDialer{}
	recordedExitCode     = -1
	recordedRunArgs      []string
	recordedRunEnv       []string
)

func mockExitRecorder(code int) {
	recordedExitCode = code
}

func mockFailingReadDir(_ string) ([]fs.DirEntry, error) {
	return nil, errMockReadDir
}

func mockNotExistReadDir(_ string) ([]fs.DirEntry, error) {
	return nil, fs.ErrNotExist
}

func mockFailingReadFile(_ string) ([]byte, error) {
	return nil, errMockReadFile
}

func mockSuccessfulExec(_ context.Context, _, _ []string) error {
	return nil
}

func mockFailingExec(_ context.Context, _, _ []string) error {
	return errMockExec
}

func mockTerminatedExec(ctx context.Context, _, _ []string) error {
	return exec.CommandContext(ctx, "sh", "-c", "kill -TERM $$").Run()
}

func mockRecordingRunExec(_ context.Context, args, env []string) error {
	recordedRunArgs = args
	recordedRunEnv = env
	return nil
}

func mockProcPath(pid int, entry string) string {
	if mockProcDirectory != "" {
		return filepath.Join(mockProcDirectory, strconv.Itoa(pid), entry)
	}
	return defaultProcPath(pid, entry)
}

func mockFailingOpenFile(_ string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, errMockOpen
}

func mockPermissionOpenFile(_ string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, os.ErrPermission
}

func mockPermissionReadFile(_ string) ([]byte, error) {
	return nil, os.ErrPermission
}

func mockFailingWriteMemory(_ *os.File, _ []byte, _ int64) (int, error) {
	return 0, errMockWrite
}

func mockPermissionWriteMemory(_ *os.File, _ []byte, _ int64) (int, error) {
	return 0, os.ErrPermission
}

func mockFailingCloseFile(_ *os.File) error {
	return errMockClose
}

func mockPermissionCloseFile(_ *os.File) error {
	return os.ErrPermission
}

func mockFailingScrub(_ context.Context, _ int) error {
	return errMockScrub
}

func mockSuccessScrub(_ context.Context, _ int) error {
	return nil
}

type mockSocketDialer struct {
	conn net.Conn
	err  error
}

func (m *mockSocketDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.conn, nil
}

func mockCreateDialer() socketDialer {
	return mockDialerInstance
}

type mockSuccessConn struct {
	net.Conn
}

func (m *mockSuccessConn) Close() error {
	return nil
}

func mockSuccessDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return &mockSuccessConn{}, nil
}

func mockFailingDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return nil, errMockDial
}

type mockErrorCloserConn struct {
	net.Conn
}

func (m *mockErrorCloserConn) Close() error {
	return errMockClose
}

func mockErrorCloserDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return &mockErrorCloserConn{}, nil
}

func mockRetrySuccessDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	mockDialAttemptCount++
	if mockDialAttemptCount == 1 {
		return nil, errMockDial
	}
	return &mockSuccessConn{}, nil
}

func mockCommandTrue(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "true")
}

func mockCommandFalse(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "false")
}

func mockCommandNonExistent(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "/path/that/does/not/exist")
}

func makeStatContent(comm, envStart, envEnd string, fieldCount int) string {
	var sb strings.Builder
	sb.WriteString("1234 (")
	sb.WriteString(comm)
	sb.WriteString(")")
	for i := range fieldCount {
		sb.WriteString(" ")
		switch i {
		case statEnvStartIndex:
			sb.WriteString(envStart)
		case statEnvEndIndex:
			sb.WriteString(envEnd)
		default:
			sb.WriteString("0")
		}
	}
	return sb.String()
}

func setupScrubChildFixture(t *testing.T) (pid int, statPath, memPath string) {
	t.Helper()
	tempDir := t.TempDir()
	pid = 4242
	pidDir := filepath.Join(tempDir, strconv.Itoa(pid))
	if err := os.Mkdir(pidDir, 0o750); err != nil {
		t.Fatalf("creating pid dir: %v", err)
	}

	statFilePath := filepath.Join(pidDir, "stat")
	memFilePath := filepath.Join(pidDir, "mem")

	validStatData := makeStatContent("ntfy", "10", "20", 49)
	if err := os.WriteFile(filepath.Clean(statFilePath), []byte(validStatData), 0o600); err != nil {
		t.Fatalf("writing stat file: %v", err)
	}

	initialMem := make([]byte, 30)
	for i := range initialMem {
		initialMem[i] = byte(i + 1)
	}
	if err := os.WriteFile(filepath.Clean(memFilePath), initialMem, 0o600); err != nil {
		t.Fatalf("writing mem file: %v", err)
	}

	mockProcDirectory = tempDir
	procPath = mockProcPath
	return pid, statFilePath, memFilePath
}

func setupScrubCleanup(t *testing.T) {
	t.Helper()
	origProcPath := procPath
	origReadFile := readFile
	origOpenFile := openFile
	origWriteMem := writeMemory
	origCloseFile := closeFile
	origDialTCP := dialTCP

	dialTCP = mockSuccessDialTCP

	t.Cleanup(func() {
		procPath = origProcPath
		readFile = origReadFile
		openFile = origOpenFile
		writeMemory = origWriteMem
		closeFile = origCloseFile
		dialTCP = origDialTCP
		mockProcDirectory = ""
	})
}

func TestDefaultReadFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "sample.txt")
	expectedContent := []byte("secret_value")

	if err := os.WriteFile(filePath, expectedContent, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	data, err := defaultReadFile(filePath)
	if err != nil {
		t.Fatalf("defaultReadFile() unexpected error: %v", err)
	}

	if !bytes.Equal(data, expectedContent) {
		t.Errorf("defaultReadFile() = %q, want %q", data, expectedContent)
	}

	if _, err := defaultReadFile(filepath.Join(tempDir, "nonexistent.txt")); err == nil {
		t.Error("defaultReadFile() expected error on nonexistent file, got nil")
	}
}

func TestDefaultReadDir(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "entry.txt")
	if err := os.WriteFile(filePath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("writing entry file: %v", err)
	}

	entries, err := defaultReadDir(tempDir)
	if err != nil {
		t.Fatalf("defaultReadDir() unexpected error: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "entry.txt" {
		t.Errorf("defaultReadDir() unexpected entries: %v", entries)
	}

	if _, err := defaultReadDir(filepath.Join(tempDir, "missing_subdir")); err == nil {
		t.Error("defaultReadDir() expected error on nonexistent directory, got nil")
	}
}

func TestDefaultCloseFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "file.txt")
	file, err := os.Create(filepath.Clean(filePath))
	if err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	if err := defaultCloseFile(file); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	if err := defaultCloseFile(file); err == nil {
		t.Fatal("expected error closing already closed file, got nil")
	}
}

func TestDefaultWriteMemory(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "mem.bin")
	cleanPath := filepath.Clean(filePath)
	file, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("creating test file: %v", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("closing file: %v", closeErr)
		}
	}()

	n, err := defaultWriteMemory(file, []byte("payload"), 0)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if n != 7 {
		t.Fatalf("expected 7 bytes written, got %d", n)
	}

	readOnlyFile, err := os.OpenFile(cleanPath, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("opening read-only file: %v", err)
	}
	defer func() {
		if closeErr := readOnlyFile.Close(); closeErr != nil {
			t.Errorf("closing read-only file: %v", closeErr)
		}
	}()

	if _, err := defaultWriteMemory(readOnlyFile, []byte("data"), 0); err == nil {
		t.Fatal("expected write error on read-only file, got nil")
	}
}

func TestDefaultOpenFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "open.txt")
	cleanPath := filepath.Clean(filePath)
	if err := os.WriteFile(cleanPath, []byte("data"), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	file, err := defaultOpenFile(cleanPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("unexpected open error: %v", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("closing file: %v", closeErr)
	}

	if _, err := defaultOpenFile(filepath.Join(tempDir, "missing.txt"), os.O_RDONLY, 0); err == nil {
		t.Fatal("expected error opening missing file, got nil")
	}
}

func TestDefaultCreateDialer(t *testing.T) {
	d := defaultCreateDialer()
	if d == nil {
		t.Fatal("expected non-nil dialer, got nil")
	}
}

func TestDefaultDialTCP(t *testing.T) {
	origDialer := createDialer
	createDialer = mockCreateDialer
	t.Cleanup(func() { createDialer = origDialer })

	mockDialerInstance.err = errMockDial
	mockDialerInstance.conn = nil
	if _, err := defaultDialTCP(context.Background(), "tcp", "127.0.0.1:8080"); err == nil {
		t.Fatal("expected dial error, got nil")
	}

	mockDialerInstance.err = nil
	mockDialerInstance.conn = &mockSuccessConn{}
	conn, err := defaultDialTCP(context.Background(), "tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn, got nil")
	}
}

func TestDefaultProcPath(t *testing.T) {
	got := defaultProcPath(42, "stat")
	expected := filepath.Join(defaultProcDir, "42", "stat")
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestDefaultCreateCommand(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
	}{
		{
			args:     nil,
			name:     "empty args invokes default serve command",
			wantArgs: []string{defaultBinaryPath, defaultCommand},
			wantCmd:  defaultBinaryPath,
		},
		{
			args:     []string{"user", "add", "admin"},
			name:     "subcommand args are passed directly",
			wantArgs: []string{defaultBinaryPath, "user", "add", "admin"},
			wantCmd:  defaultBinaryPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := defaultCreateCommand(ctx, tt.args)
			if cmd.Path != tt.wantCmd && !slices.Contains(cmd.Args, tt.wantCmd) {
				t.Errorf("unexpected command path: %s", cmd.Path)
			}
			if !slices.Equal(cmd.Args, tt.wantArgs) {
				t.Errorf("defaultCreateCommand() args = %v, want %v", cmd.Args, tt.wantArgs)
			}
		})
	}

	t.Run("cancel command returns nil when process not started", func(t *testing.T) {
		cmd := defaultCreateCommand(ctx, nil)
		if cmd.Cancel == nil {
			t.Fatal("expected Cancel function to be configured")
		}
		if err := cmd.Cancel(); err != nil {
			t.Errorf("cmd.Cancel() unexpected error: %v", err)
		}
	})

	t.Run("cancel command signals running process", func(t *testing.T) {
		cmd := exec.CommandContext(ctx, "sleep", "10")
		cancelFn := cancelCommand(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatalf("cmd.Start() unexpected error: %v", err)
		}
		defer func() {
			if waitErr := cmd.Wait(); waitErr != nil && !IsTermination(ctx, waitErr) {
				t.Errorf("cmd.Wait() unexpected error: %v", waitErr)
			}
		}()
		if err := cancelFn(); err != nil {
			t.Errorf("cancelFn() unexpected error: %v", err)
		}
	})
}

func TestParseProcStat(t *testing.T) {
	tests := []struct {
		name         string
		statContent  string
		wantEnvStart int64
		wantEnvEnd   int64
		expectErr    bool
	}{
		{
			expectErr:   true,
			name:        "missing closing comm paren",
			statContent: "1234 ntfy 0 0 0",
		},
		{
			expectErr:   true,
			name:        "insufficient fields after comm",
			statContent: makeStatContent("ntfy", "100", "200", 20),
		},
		{
			expectErr:   true,
			name:        "invalid env start non number",
			statContent: makeStatContent("ntfy", "not_a_number", "200", 49),
		},
		{
			expectErr:   true,
			name:        "invalid env end non number",
			statContent: makeStatContent("ntfy", "100", "not_a_number", 49),
		},
		{
			expectErr:   true,
			name:        "negative or zero env start",
			statContent: makeStatContent("ntfy", "0", "200", 49),
		},
		{
			expectErr:   true,
			name:        "env start greater than env end",
			statContent: makeStatContent("ntfy", "300", "200", 49),
		},
		{
			expectErr:   true,
			name:        "env range exceeds max limit",
			statContent: makeStatContent("ntfy", "1000", "34554433", 49),
		},
		{
			expectErr:    false,
			name:         "valid stat with nested comm",
			statContent:  makeStatContent("ntfy (worker) proc", "1000", "2000", 49),
			wantEnvEnd:   2000,
			wantEnvStart: 1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envStart, envEnd, err := parseProcStat(tt.statContent)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for case %q, got nil", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for case %q: %v", tt.name, err)
			}
			if envStart != tt.wantEnvStart || envEnd != tt.wantEnvEnd {
				t.Fatalf("expected (%d, %d), got (%d, %d)", tt.wantEnvStart, tt.wantEnvEnd, envStart, envEnd)
			}
		})
	}
}

func TestResolveReadinessAddress(t *testing.T) {
	tests := []struct {
		custom   string
		envValue string
		name     string
	}{
		{
			custom:   "127.0.0.1:9090",
			envValue: ":9090",
			name:     "colon port prefix",
		},
		{
			envValue: "0.0.0.0:8080",
			name:     "host port pair",
		},
		{
			envValue: "8080",
			name:     "numeric port only",
		},
		{
			envValue: "",
			name:     "empty environment variable",
		},
		{
			envValue: "invalid_address",
			name:     "malformed address string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("NTFY_LISTEN_HTTP", tt.envValue)
			}
			expected := tt.custom
			if expected == "" {
				expected = "127.0.0.1:8080"
			}
			addr := resolveReadinessAddress()
			if addr != expected {
				t.Errorf("resolveReadinessAddress() = %q, want %q", addr, expected)
			}
		})
	}
}

func TestWaitForChildReadiness(t *testing.T) {
	origDial := dialTCP
	t.Cleanup(func() { dialTCP = origDial })

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForChildReadiness(canceledCtx, "127.0.0.1:8080", time.Second); err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}

	dialTCP = mockSuccessDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:8080", time.Second); err != nil {
		t.Fatalf("unexpected error on ready socket: %v", err)
	}

	dialTCP = mockErrorCloserDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:8080", time.Second); err == nil {
		t.Fatal("expected error when socket close fails, got nil")
	}

	dialTCP = mockFailingDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:8080", -1*time.Second); err != nil {
		t.Fatalf("expected nil when deadline passed, got %v", err)
	}

	ctxWait, cancelWait := context.WithCancel(context.Background())
	time.AfterFunc(2*time.Millisecond, cancelWait)
	if err := waitForChildReadiness(ctxWait, "127.0.0.1:8080", 2*time.Second); err == nil {
		t.Fatal("expected error when context canceled during wait, got nil")
	}

	mockDialAttemptCount = 0
	dialTCP = mockRetrySuccessDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:8080", time.Second); err != nil {
		t.Fatalf("unexpected error on retry dial: %v", err)
	}
}

func TestIsIgnorableMemoryError(t *testing.T) {
	tests := []struct {
		err      error
		name     string
		expected bool
	}{
		{
			err:      fs.ErrNotExist,
			expected: true,
			name:     "not exist error",
		},
		{
			err:      fs.ErrPermission,
			expected: true,
			name:     "permission error",
		},
		{
			err:      errors.New("other unhandled failure"),
			expected: false,
			name:     "generic failure error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIgnorableMemoryError(tt.err); got != tt.expected {
				t.Errorf("isIgnorableMemoryError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestDefaultScrubChildMemory_ReadinessErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := defaultScrubChildMemory(canceledCtx, pid); err != nil {
		t.Fatalf("expected nil when readiness context canceled, got %v", err)
	}

	dialTCP = mockErrorCloserDialTCP
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on readiness failure, got nil")
	}
}

func TestDefaultScrubChildMemory_ReadAndParseErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, statFilePath, _ := setupScrubChildFixture(t)

	if err := defaultScrubChildMemory(context.Background(), 9999); err != nil {
		t.Fatalf("expected nil when stat file missing, got %v", err)
	}

	readFile = mockPermissionReadFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on stat read permission error, got %v", err)
	}

	readFile = mockFailingReadFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic stat read error, got nil")
	}
	readFile = defaultReadFile

	if err := os.WriteFile(filepath.Clean(statFilePath), []byte("corrupt stat data"), 0o600); err != nil {
		t.Fatalf("overwriting stat: %v", err)
	}
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on corrupt stat data, got nil")
	}
}

func TestDefaultScrubChildMemory_OpenErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, memFilePath := setupScrubChildFixture(t)

	if err := os.Remove(filepath.Clean(memFilePath)); err != nil {
		t.Fatalf("removing mem file: %v", err)
	}
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil when mem file missing, got %v", err)
	}

	openFile = mockPermissionOpenFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem open permission error, got %v", err)
	}

	openFile = mockFailingOpenFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem open error, got nil")
	}
}

func TestDefaultScrubChildMemory_WriteErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	writeMemory = mockPermissionWriteMemory
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem write permission error, got %v", err)
	}

	writeMemory = mockFailingWriteMemory
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem write error, got nil")
	}
}

func TestDefaultScrubChildMemory_CloseErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	closeFile = mockPermissionCloseFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem close permission error, got %v", err)
	}

	closeFile = mockFailingCloseFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem close error, got nil")
	}
}

func TestDefaultScrubChildMemory_Success(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, memFilePath := setupScrubChildFixture(t)

	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("unexpected error on successful scrub: %v", err)
	}

	scrubbedData, err := os.ReadFile(filepath.Clean(memFilePath))
	if err != nil {
		t.Fatalf("reading scrubbed mem file: %v", err)
	}
	for i := range 10 {
		if scrubbedData[i] != byte(i+1) {
			t.Fatalf("expected untouched byte at %d, got %d", i, scrubbedData[i])
		}
	}
	for i := 10; i < 20; i++ {
		if scrubbedData[i] != 0 {
			t.Fatalf("expected zeroed byte at %d, got %d", i, scrubbedData[i])
		}
	}
	for i := 20; i < 30; i++ {
		if scrubbedData[i] != byte(i+1) {
			t.Fatalf("expected untouched byte at %d, got %d", i, scrubbedData[i])
		}
	}
}

func TestDefaultExec(t *testing.T) {
	origCreateCommand := createCommand
	origScrub := scrubChildMemory
	defer func() {
		createCommand = origCreateCommand
		scrubChildMemory = origScrub
	}()

	ctx := context.Background()

	tests := []struct {
		cmdFunc     func(context.Context, []string) *exec.Cmd
		scrubFunc   func(context.Context, int) error
		name        string
		expectError bool
	}{
		{
			cmdFunc:     mockCommandTrue,
			expectError: false,
			name:        "successful execution with working scrub",
			scrubFunc:   mockSuccessScrub,
		},
		{
			cmdFunc:     mockCommandTrue,
			expectError: false,
			name:        "successful execution with failing scrub logs warning",
			scrubFunc:   mockFailingScrub,
		},
		{
			cmdFunc:     mockCommandFalse,
			expectError: true,
			name:        "command exit failure propagates error",
			scrubFunc:   mockSuccessScrub,
		},
		{
			cmdFunc:     mockCommandNonExistent,
			expectError: true,
			name:        "missing binary execution failure",
			scrubFunc:   mockSuccessScrub,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createCommand = tt.cmdFunc
			scrubChildMemory = tt.scrubFunc
			err := defaultExec(ctx, []string{"test"}, []string{"FOO=BAR"})
			if tt.expectError && err == nil {
				t.Errorf("defaultExec() expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("defaultExec() unexpected error: %v", err)
			}
		})
	}
}

func TestPrepareEnvironment_Errors(t *testing.T) {
	origReadDir := readDir
	origReadFile := readFile
	defer func() {
		readDir = origReadDir
		readFile = origReadFile
	}()

	tempDir := t.TempDir()

	tests := []struct {
		setup       func()
		expectedErr error
		name        string
		targetDir   string
	}{
		{
			expectedErr: errMockReadDir,
			name:        "readDir error propagates",
			setup: func() {
				readDir = mockFailingReadDir
			},
			targetDir: tempDir,
		},
		{
			expectedErr: nil,
			name:        "missing directory returns base environment",
			setup: func() {
				readDir = origReadDir
			},
			targetDir: filepath.Join(tempDir, "absent_dir"),
		},
		{
			expectedErr: errMockReadFile,
			name:        "readFile error propagates",
			setup: func() {
				readDir = origReadDir
				readFile = mockFailingReadFile
				if err := os.WriteFile(filepath.Join(tempDir, "ntfy_token"), []byte("value"), 0o600); err != nil {
					t.Fatalf("writing test secret file: %v", err)
				}
			},
			targetDir: tempDir,
		},
		{
			expectedErr: errInvalidSecretKey,
			name:        "invalid secret key returns error",
			setup: func() {
				readDir = origReadDir
				readFile = origReadFile
				if err := os.WriteFile(filepath.Join(tempDir, "ntfy_bad=key"), []byte("val"), 0o600); err != nil {
					t.Fatalf("writing test secret file: %v", err)
				}
			},
			targetDir: tempDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			res, err := PrepareEnvironment([]string{"BASE=1"}, tt.targetDir)
			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Fatalf("expected error %v, got %v", tt.expectedErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !slices.Contains(res, "BASE=1") {
					t.Errorf("expected base env preserved, got %v", res)
				}
			}
		})
	}
}

func TestPrepareEnvironment_Scanning(t *testing.T) {
	origReadDir := readDir
	origReadFile := readFile
	defer func() {
		readDir = origReadDir
		readFile = origReadFile
	}()

	readDir = defaultReadDir
	readFile = defaultReadFile

	tempDir := t.TempDir()

	if err := os.Mkdir(filepath.Join(tempDir, "ntfy_directory"), 0o700); err != nil {
		t.Fatalf("creating test subdir: %v", err)
	}

	files := []struct {
		filename string
		content  string
	}{
		{filename: "ignored_file.txt", content: "ignore"},
		{filename: "ntfy_", content: "empty_key"},
		{filename: "ntfy_empty_val", content: "   \n\t  "},
		{filename: "ntfy_base_url", content: "https://ntfy.example.com\n"},
		{filename: "ntfy_auth_default_access", content: "deny-all"},
	}

	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tempDir, f.filename), []byte(f.content), 0o600); err != nil {
			t.Fatalf("creating test file %s: %v", f.filename, err)
		}
	}

	baseEnv := []string{"INITIAL=VAL"}
	res, err := PrepareEnvironment(baseEnv, tempDir)
	if err != nil {
		t.Fatalf("PrepareEnvironment() unexpected error: %v", err)
	}

	expectedMatches := []string{
		"INITIAL=VAL",
		"NTFY_BASE_URL=https://ntfy.example.com",
		"NTFY_AUTH_DEFAULT_ACCESS=deny-all",
	}

	for _, match := range expectedMatches {
		if !slices.Contains(res, match) {
			t.Errorf("missing expected environment entry %q in %v", match, res)
		}
	}

	unexpectedPrefixes := []string{
		"NTFY_DIRECTORY",
		"IGNORED_FILE",
		"NTFY_EMPTY_VAL",
		"NTFY_=",
	}

	for _, prefix := range unexpectedPrefixes {
		for _, envEntry := range res {
			if slices.Contains([]string{envEntry}, prefix) {
				t.Errorf("unexpected environment entry found: %s", envEntry)
			}
		}
	}
}

func TestBuildCommandArguments(t *testing.T) {
	tests := []struct {
		name       string
		defaultCmd string
		args       []string
		want       []string
	}{
		{
			args:       nil,
			defaultCmd: defaultCommand,
			name:       "empty input produces default daemon command invocation",
			want:       []string{defaultCommand},
		},
		{
			args:       []string{"access", "alice", "alerts"},
			defaultCmd: defaultCommand,
			name:       "custom command arguments are returned as subcommands",
			want:       []string{"access", "alice", "alerts"},
		},
		{
			args:       []string{defaultBinaryPath, "token", "info"},
			defaultCmd: defaultCommand,
			name:       "explicit binary argument is stripped and returns subcommands",
			want:       []string{"token", "info"},
		},
		{
			args:       []string{defaultBinaryPath},
			defaultCmd: defaultCommand,
			name:       "explicit binary argument alone falls back to default command",
			want:       []string{defaultCommand},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildCommandArguments(tt.defaultCmd, tt.args)
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildCommandArguments() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		err      error
		name     string
		expected int
	}{
		{
			err:      nil,
			expected: 0,
			name:     "nil error returns zero exit code",
		},
		{
			err:      errors.New("generic failure"),
			expected: 1,
			name:     "generic error returns exit code one",
		},
		{
			err: func() error {
				cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit 42")
				return cmd.Run()
			}(),
			expected: 42,
			name:     "exit error extracts subprocess status code",
		},
		{
			err:      exec.CommandContext(t.Context(), "sh", "-c", "kill -TERM $$").Run(),
			expected: 0,
			name:     "termination signal returns zero exit code",
		},
		{
			err:      exec.CommandContext(t.Context(), "sh", "-c", "kill -INT $$").Run(),
			expected: 0,
			name:     "interrupt signal returns zero exit code",
		},
		{
			err:      exec.CommandContext(t.Context(), "sh", "-c", "kill -ABRT $$").Run(),
			expected: 134,
			name:     "other signal returns 128 plus signal code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.expected {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.expected)
			}
		})
	}
}

func TestIsTermination(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		ctx  context.Context
		err  error
		name string
		want bool
	}{
		{
			ctx:  t.Context(),
			err:  nil,
			name: "nil error with active context",
			want: false,
		},
		{
			ctx:  t.Context(),
			err:  errors.New("standard error"),
			name: "standard error with active context",
			want: false,
		},
		{
			ctx:  canceledCtx,
			err:  context.Canceled,
			name: "canceled context error",
			want: true,
		},
		{
			ctx:  t.Context(),
			err:  exec.CommandContext(t.Context(), "sh", "-c", "kill -TERM $$").Run(),
			name: "subprocess killed by sigterm",
			want: true,
		},
		{
			ctx:  t.Context(),
			err:  exec.CommandContext(t.Context(), "sh", "-c", "kill -INT $$").Run(),
			name: "subprocess killed by sigint",
			want: true,
		},
		{
			ctx:  t.Context(),
			err:  exec.CommandContext(t.Context(), "sh", "-c", "kill -KILL $$").Run(),
			name: "subprocess killed by sigkill",
			want: true,
		},
		{
			ctx:  t.Context(),
			err:  exec.CommandContext(t.Context(), "false").Run(),
			name: "subprocess non-signal failure",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTermination(tc.ctx, tc.err); got != tc.want {
				t.Errorf("IsTermination() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	origReadDir := readDir
	origReadFile := readFile
	origExecProcess := execProcess
	defer func() {
		readDir = origReadDir
		readFile = origReadFile
		execProcess = origExecProcess
	}()

	tempDir := t.TempDir()
	readDir = defaultReadDir
	readFile = defaultReadFile

	tests := []struct {
		execFunc    func(context.Context, []string, []string) error
		readDirFunc func(string) ([]fs.DirEntry, error)
		name        string
		expectError bool
	}{
		{
			expectError: false,
			execFunc:    mockRecordingRunExec,
			name:        "successful execution passes arguments and environment",
			readDirFunc: defaultReadDir,
		},
		{
			expectError: true,
			execFunc:    mockRecordingRunExec,
			name:        "environment preparation error propagates failure",
			readDirFunc: mockFailingReadDir,
		},
		{
			expectError: true,
			execFunc:    mockFailingExec,
			name:        "process execution failure propagates error",
			readDirFunc: defaultReadDir,
		},
	}

	runArgs := []string{"publish", "topic1", "hello"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recordedRunArgs = nil
			recordedRunEnv = nil
			execProcess = tt.execFunc
			readDir = tt.readDirFunc

			err := Run(context.Background(), runArgs, tempDir)
			if tt.expectError && err == nil {
				t.Fatalf("Run() expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("Run() unexpected error: %v", err)
			}
			if !tt.expectError {
				if !reflect.DeepEqual(recordedRunArgs, runArgs) {
					t.Errorf("Run() args = %v, want %v", recordedRunArgs, runArgs)
				}
				if len(recordedRunEnv) == 0 {
					t.Errorf("Run() expected non-empty environment")
				}
			}
		})
	}
}

func TestMain_Success(t *testing.T) {
	origExecProcess := execProcess
	origReadDir := readDir
	origReadFile := readFile
	origExitFunc := exitFunc
	origArgs := os.Args
	defer func() {
		execProcess = origExecProcess
		readDir = origReadDir
		readFile = origReadFile
		exitFunc = origExitFunc
		os.Args = origArgs
	}()

	execProcess = mockSuccessfulExec
	readDir = mockNotExistReadDir
	recordedExitCode = -1
	exitFunc = mockExitRecorder
	os.Args = []string{"init", "serve"}

	main()

	if recordedExitCode != -1 {
		t.Errorf("main() unexpected exit code: %d", recordedExitCode)
	}
}

func TestMain_Failure(t *testing.T) {
	origExecProcess := execProcess
	origReadDir := readDir
	origReadFile := readFile
	origExitFunc := exitFunc
	defer func() {
		execProcess = origExecProcess
		readDir = origReadDir
		readFile = origReadFile
		exitFunc = origExitFunc
	}()

	readDir = mockFailingReadDir
	recordedExitCode = -1
	exitFunc = mockExitRecorder

	main()

	if recordedExitCode != 1 {
		t.Errorf("main() expected exit code 1 on failure, got %d", recordedExitCode)
	}
}

func TestMain_Termination(t *testing.T) {
	origExecProcess := execProcess
	origReadDir := readDir
	origReadFile := readFile
	origExitFunc := exitFunc
	defer func() {
		execProcess = origExecProcess
		readDir = origReadDir
		readFile = origReadFile
		exitFunc = origExitFunc
	}()

	execProcess = mockTerminatedExec
	readDir = defaultReadDir
	recordedExitCode = -1
	exitFunc = mockExitRecorder

	main()

	if recordedExitCode != 0 {
		t.Errorf("main() on termination expected exit code 0, got %d", recordedExitCode)
	}
}
