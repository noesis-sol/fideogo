package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var videoExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".m4v":  true,
	".webm": true,
}

type videoFile struct {
	path     string
	name     string
	selected bool
	status   string // "", "processing", "done", "error"
	progress float64
	info     string
	outInfo  string
	err      error // Error for this specific file
}

func findVideos(dir string) []videoFile {
	var files []videoFile

	if dir == "" {
		return files
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return files
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}

	entryNames := make(map[string]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			entryNames[entry.Name()] = true
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !videoExtensions[ext] {
			continue
		}

		// Skip output files only if a corresponding source file exists.
		if strings.HasPrefix(name, outputPrefix) {
			sourceName := strings.TrimPrefix(name, outputPrefix)
			if entryNames[sourceName] {
				continue
			}
			// Also check if source exists with a different extension (format conversion).
			sourceBase := strings.TrimSuffix(sourceName, filepath.Ext(sourceName))
			hasSource := false
			for srcExt := range videoExtensions {
				if entryNames[sourceBase+srcExt] {
					hasSource = true
					break
				}
			}
			if hasSource {
				continue
			}
		}

		files = append(files, videoFile{
			path: filepath.Join(dir, name),
			name: name,
		})
	}
	return files
}

func collectVideosFromPattern(pattern string) ([]videoFile, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("error expanding pattern: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no files or directories match pattern: %s", pattern)
	}

	var allFiles []videoFile
	seen := make(map[string]bool)
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			continue
		}

		if info.IsDir() {
			dirFiles := findVideos(match)
			for _, f := range dirFiles {
				if !seen[f.path] {
					seen[f.path] = true
					allFiles = append(allFiles, f)
				}
			}
			continue
		}

		ext := strings.ToLower(filepath.Ext(match))
		if !videoExtensions[ext] {
			continue
		}
		absPath, err := filepath.Abs(match)
		if err != nil {
			continue
		}
		if !seen[absPath] {
			seen[absPath] = true
			allFiles = append(allFiles, videoFile{
				path: absPath,
				name: filepath.Base(match),
			})
		}
	}

	if len(allFiles) == 0 {
		return nil, fmt.Errorf("no video files found matching pattern: %s", pattern)
	}
	return allFiles, nil
}
