package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Helper function to create a temporary directory structure for testing
func createTestDir(t *testing.T, files map[string]string) string {
	tmpDir, err := os.MkdirTemp("", "spacehogs_test_")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		if strings.HasSuffix(path, "/") { // Indicates a directory
			err := os.MkdirAll(fullPath, 0755)
			if err != nil {
				t.Fatalf("Failed to create dir %s: %v", fullPath, err)
			}
		} else {
			dir := filepath.Dir(fullPath)
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				t.Fatalf("Failed to create parent dir %s: %v", dir, err)
			}
			err = os.WriteFile(fullPath, []byte(content), 0644)
			if err != nil {
				t.Fatalf("Failed to create file %s: %v", fullPath, err)
			}
		}
	}
	return tmpDir
}

// Reset results and resultsMutex for each test
func resetResults() {
	resultsMutex.Lock()
	results = nil
	resultsMutex.Unlock()
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		input    string
		expected uint64
		hasError bool
	}{
		{"100B", 100, false},
		{"100b", 100, false},
		{"10K", 10 * 1024, false},
		{"10k", 10 * 1024, false},
		{"1.5M", uint64(1.5 * 1024 * 1024), false},
		{"2G", 2 * 1024 * 1024 * 1024, false},
		{"1T", 1024 * 1024 * 1024 * 1024, false},
		{"100", 100, false}, // No unit, assumes bytes
		{"", 0, true},       // Empty string
		{"abc", 0, true},    // Invalid format
		{"100XYZ", 0, true}, // Invalid unit
		{"-50M", 0, true},   // Negative value (parsefloat handles this, but the logic should handle it)
	}

	for _, test := range tests {
		size, err := parseSize(test.input)
		if (err != nil) != test.hasError {
			t.Errorf("For input '%s', expected error: %v, got: %v", test.input, test.hasError, err != nil)
		}
		if !test.hasError && size != test.expected {
			t.Errorf("For input '%s', expected size: %d, got: %d", test.input, test.expected, size)
		}
	}
}

func TestHumanReadableSize(t *testing.T) {
	tests := []struct {
		input    uint64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.00 KiB"},
		{1536, "1.50 KiB"}, // 1.5 KB
		{1024 * 1024, "1.00 MiB"},
		{1.5 * 1024 * 1024, "1.50 MiB"},
		{2 * 1024 * 1024 * 1024, "2.00 GiB"},
		{1024 * 1024 * 1024 * 1024, "1.00 TiB"},
		{2345678901234, "2.13 TiB"},
	}

	for _, test := range tests {
		result := humanReadableSize(test.input)
		if result != test.expected {
			t.Errorf("For input %d, expected '%s', got '%s'", test.input, test.expected, result)
		}
	}
}

func TestWalkDirRecursive(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string]string
		threshold   uint64
		exclude     []string
		expected    []FileInfo
		expectedSum uint64 // total size returned by walkDirRecursive
	}{
		{
			name: "basic traversal with small files",
			files: map[string]string{
				"file1.txt":      "hello", // 5 bytes
				"subdir1/file2.txt": "world", // 5 bytes
				"subdir2/file3.txt": "!",     // 1 byte
			},
			threshold: 1, // All files are >= 1 byte
			exclude:   []string{},
			expected: []FileInfo{
				{Path: "file1.txt", Size: 5, IsDir: false},
				{Path: "subdir1/file2.txt", Size: 5, IsDir: false},
				{Path: "subdir2/file3.txt", Size: 1, IsDir: false},
				{Path: "subdir1", Size: 5, IsDir: true}, // subdir1 contains only file2.txt
				{Path: "subdir2", Size: 1, IsDir: true}, // subdir2 contains only file3.txt
			},
			expectedSum: 11, // 5 + 5 + 1
		},
		{
			name: "threshold filtering - files below threshold",
			files: map[string]string{
				"file1.txt": "hello", // 5 bytes
				"file2.txt": "w",     // 1 byte
			},
			threshold: 5,
			exclude:   []string{},
			expected: []FileInfo{
				{Path: "file1.txt", Size: 5, IsDir: false},
			},
			expectedSum: 6, // file1.txt (5) + file2.txt (1)
		},
		{
			name: "threshold filtering - directory below threshold",
			files: map[string]string{
				"dirA/fileA.txt": "aaa", // 3 bytes
				"dirB/fileB.txt": "bbbbbb", // 6 bytes
			},
			threshold: 5,
			exclude:   []string{},
			expected: []FileInfo{
				{Path: "dirB/fileB.txt", Size: 6, IsDir: false},
				{Path: "dirB", Size: 6, IsDir: true},
			},
			expectedSum: 9, // dirA (3) + dirB (6)
		},
		{
			name: "exclude directories",
			files: map[string]string{
				"included_dir/file1.txt": "111", // 3 bytes
				"excluded_dir/file2.txt": "2222", // 4 bytes
				"another_file.txt":       "33333", // 5 bytes
			},
			threshold: 1,
			exclude:   []string{"excluded_dir"},
			expected: []FileInfo{
				{Path: "included_dir/file1.txt", Size: 3, IsDir: false},
				{Path: "another_file.txt", Size: 5, IsDir: false},
				{Path: "included_dir", Size: 3, IsDir: true},
			},
			expectedSum: 8, // included_dir (3) + another_file.txt (5)
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetResults()
			tmpDir := createTestDir(t, test.files)
			defer os.RemoveAll(tmpDir)

			excludeSet := make(map[string]struct{})
			for _, e := range test.exclude {
				excludeSet[e] = struct{}{}
			}

			actualSum := walkDirRecursive(tmpDir, test.threshold, excludeSet)

			// Clean up paths in expected results to be relative to tmpDir
			for i := range test.expected {
				test.expected[i].Path = filepath.Join(tmpDir, test.expected[i].Path)
			}

			// Sort actual and expected results for comparison
			sort.Slice(results, func(i, j int) bool {
				if results[i].IsDir != results[j].IsDir {
					return results[i].IsDir
				}
				if results[i].Size != results[j].Size {
					return results[i].Size > results[j].Size
				}
				return results[i].Path < results[j].Path
			})
			sort.Slice(test.expected, func(i, j int) bool {
				if test.expected[i].IsDir != test.expected[j].IsDir {
					return test.expected[i].IsDir
				}
				if test.expected[i].Size != test.expected[j].Size {
					return test.expected[i].Size > test.expected[j].Size
				}
				return test.expected[i].Path < test.expected[j].Path
			})

			if !reflect.DeepEqual(results, test.expected) {
				t.Errorf("WalkDirRecursive() results mismatch.\nExpected:\n%v\nActual:\n%v", test.expected, results)
			}
			if actualSum != test.expectedSum {
				t.Errorf("WalkDirRecursive() total sum mismatch.\nExpected: %d\nActual: %d", test.expectedSum, actualSum)
			}
		})
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		setup       func(t *testing.T) (string, func()) // Returns setup path and a cleanup function
		expectError bool
		errorContains string
		expectedOutputContains []string
	}{
		{
			name: "invalid number of arguments",
			args: []string{"spacehogs", "onedir"}, // Missing min_size
			expectError: true,
			errorContains: "invalid number of arguments",
		},
		{
			name: "invalid size argument",
			args: []string{"spacehogs", ".", "10MBB"}, // Invalid size format
			expectError: true,
			errorContains: "invalid size format",
		},
		{
			name: "invalid path argument - non-existent",
			args: []string{"spacehogs", "/no/such/dir", "1K"},
			expectError: true,
			errorContains: "no such file or directory",
		},
		{
			name: "path is not a directory",
			args: []string{"spacehogs", "testfile.txt", "1K"},
			setup: func(t *testing.T) (string, func()) {
				// Create a dummy file to be used as a path
				file, err := os.Create("testfile.txt")
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
				file.Close()
				return "", func() { os.Remove("testfile.txt") }
			},
			expectError: true,
			errorContains: "is not a directory",
		},
		{
			name: "top-level directory exclusion",
			args: []string{"spacehogs", "-exclude=excluded_dir", "excluded_dir", "1K"},
			setup: func(t *testing.T) (string, func()) {
				err := os.Mkdir("excluded_dir", 0755)
				if err != nil {
					t.Fatalf("Failed to create dir: %v", err)
				}
				return "", func() { os.RemoveAll("excluded_dir") }
			},
			expectError: false, // No error, just a message to stdout
		},
		{
			name: "successful run with output",
			args: []string{"spacehogs", "TMP_DIR", "1K"},
			setup: func(t *testing.T) (string, func()) {
				tmpDir := createTestDir(t, map[string]string{
					"file1.txt":        "hello",
					"subdir/file2.txt": strings.Repeat("a", 2048),
				})
				return tmpDir, func() { os.RemoveAll(tmpDir) }
			},
			expectError: false,
			expectedOutputContains: []string{
				"[DIR] ",
				"2.00 KiB",
				"subdir",
				"[FILE]",
				"2.00 KiB",
				"subdir/file2.txt",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetResults()
			var tmpDir string
			if test.setup != nil {
				var cleanup func()
				tmpDir, cleanup = test.setup(t)
				defer cleanup()
			}

			// Replace placeholder in args
			args := make([]string, len(test.args))
			copy(args, test.args)
			for i, arg := range args {
				if arg == "TMP_DIR" {
					args[i] = tmpDir
				}
			}

			// Redirect stdout to capture output.
			oldStdout := os.Stdout
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stdout = w
			os.Stderr = w

			err := run(args)

			w.Close()
			os.Stdout = oldStdout
			os.Stderr = oldStderr
			
			output, readErr := io.ReadAll(r)
			if readErr != nil {
				t.Fatalf("failed to read from pipe: %v", readErr)
			}
			outputStr := string(output)

			if (err != nil) != test.expectError {
				t.Errorf("run() error = %v, expectError %v", err, test.expectError)
			}
			if err != nil && test.errorContains != "" {
				if !strings.Contains(err.Error(), test.errorContains) {
					t.Errorf("run() error = %q, expected to contain %q", err.Error(), test.errorContains)
				}
			}

			for _, expected := range test.expectedOutputContains {
				// We need to join the tmpDir to the expected path for comparison
				if strings.Contains(expected, "subdir") {
					expected = filepath.Join(tmpDir, expected)
				}
				if !strings.Contains(outputStr, expected) {
					t.Errorf("run() output = %q, did not contain %q", outputStr, expected)
				}
			}
		})
	}
}
