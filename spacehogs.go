package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// FileInfo holds information about a file or directory.
type FileInfo struct {
	Path  string
	Size  uint64
	IsDir bool
}

var (
	results      []FileInfo
	resultsMutex sync.Mutex
)

// parseSize converts a human-readable size string (e.g., "100M", "2G") to bytes.
func parseSize(sizeStr string) (uint64, error) {
	re := regexp.MustCompile(`(?i)^([\d\.]+)\s*([kmgtp]?b?)$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(sizeStr))
	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid size format: %s", sizeStr)
	}

	size, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size number: %s", matches[1])
	}

	unit := strings.ToUpper(matches[2])
	switch {
	case strings.HasPrefix(unit, "T"):
		size *= 1024 * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "G"):
		size *= 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "M"):
		size *= 1024 * 1024
	case strings.HasPrefix(unit, "K"):
		size *= 1024
	}

	return uint64(size), nil
}

// humanReadableSize converts a size in bytes to a human-readable string.
func humanReadableSize(size uint64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	const unit = 1024
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

// addResult adds a file or directory to the results slice in a thread-safe manner.
func addResult(path string, size uint64, isDir bool) {
	resultsMutex.Lock()
	results = append(results, FileInfo{Path: path, Size: size, IsDir: isDir})
	resultsMutex.Unlock()
}

// walkDirRecursive performs a parallel, post-order traversal of a directory tree.
func walkDirRecursive(path string, threshold uint64, excludeSet map[string]struct{}) uint64 {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading directory %s: %v\n", path, err)
		return 0
	}

	var totalSize uint64
	var wg sync.WaitGroup
	sizeChannel := make(chan uint64, len(entries))

	for _, entry := range entries {
		// Check if the directory/file name is in the exclude set
		if _, excluded := excludeSet[entry.Name()]; excluded {
			continue // Skip this entry completely
		}

		fullPath := filepath.Join(path, entry.Name())

		if entry.IsDir() {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				subdirSize := walkDirRecursive(p, threshold, excludeSet)
				if subdirSize >= threshold {
					addResult(p, subdirSize, true)
				}
				sizeChannel <- subdirSize
			}(fullPath)
		} else {
			info, err := entry.Info()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting info for %s: %v\n", fullPath, err)
				continue
			}
			fileSize := uint64(info.Size())
			if fileSize >= threshold {
				addResult(fullPath, fileSize, false)
			}
			totalSize += fileSize
		}
	}

	// Wait for all subdirectory goroutines to finish
	wg.Wait()
	close(sizeChannel)

	// Collect all subdirectory sizes from the channel
	for size := range sizeChannel {
		totalSize += size
	}

	return totalSize
}


func run(args []string) error {
	fs := flag.NewFlagSet("spacehogs", flag.ContinueOnError)
	var excludeDirs string
	fs.StringVar(&excludeDirs, "exclude", "proc,dev,sys", "Comma-separated list of directory names to exclude")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <directory> <min_size>\n", args[0])
		fmt.Fprintf(os.Stderr, "Size format: number[unit] (e.g., 100M, 1.5G)\n")
		fmt.Fprintf(os.Stderr, "Units: B, K, M, G, T, P\n\n")
		fmt.Println("Options:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if fs.NArg() != 2 {
		fs.Usage()
		return fmt.Errorf("invalid number of arguments")
	}

	// Build the exclude set
	excludeSet := make(map[string]struct{})
	if excludeDirs != "" {
		for _, dir := range strings.Split(excludeDirs, ",") {
			trimmed := strings.TrimSpace(dir)
			if trimmed != "" {
				excludeSet[trimmed] = struct{}{}
			}
		}
	}

	scanPath := fs.Arg(0)
	minSizeStr := fs.Arg(1)

	// Clean the path to remove any trailing slashes for consistent output
	scanPath = filepath.Clean(scanPath)

	// Check if the top-level directory itself is excluded
	if _, excluded := excludeSet[filepath.Base(scanPath)]; excluded {
		fmt.Printf("Top-level directory '%s' is in the exclude list. Nothing to do.\n", scanPath)
		return nil
	}

	threshold, err := parseSize(minSizeStr)
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}

	fi, err := os.Stat(scanPath)
	if err != nil {
		return fmt.Errorf("error accessing '%s': %v", scanPath, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("error: '%s' is not a directory", scanPath)
	}

	hrThreshold := humanReadableSize(threshold)
	fmt.Printf("Scanning directory: %s\n", scanPath)
	fmt.Printf("Minimum size threshold: %s\n", hrThreshold)
	if len(excludeSet) > 0 {
		fmt.Printf("Excluding: %s\n", excludeDirs)
	}
	fmt.Println("\nTYPE   SIZE        NAME")
	fmt.Println("--------------------------------")

	// Start the recursive scan.
	totalSize := walkDirRecursive(scanPath, threshold, excludeSet)

	// Add the top-level directory to the results if it meets the threshold
	if totalSize >= threshold {
		addResult(scanPath, totalSize, true)
	}

	// Sort results: directories first, then by size descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].IsDir != results[j].IsDir {
			return results[i].IsDir
		}
		if results[i].Size != results[j].Size {
			return results[i].Size > results[j].Size
		}
		return results[i].Path < results[j].Path
	})

	// Display results
	for _, res := range results {
		typeStr := "[FILE]"
		if res.IsDir {
			typeStr = "[DIR] "
		}
		fmt.Printf("%s %-10s  %s\n",
			typeStr,
			humanReadableSize(res.Size),
			res.Path)
	}
	return nil
}

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
