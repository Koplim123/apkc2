package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const cachePath = "build/.incremental"

type buildCache struct {
	Res        string           `json:"res,omitempty"`
	Src        string           `json:"src,omitempty"`
	Classes    string           `json:"classes,omitempty"`
	ResTimes   map[string]int64 `json:"res_times,omitempty"`
	SrcTimes   map[string]int64 `json:"src_times,omitempty"`
	ClassTimes map[string]int64 `json:"class_times,omitempty"`
}

func loadCache() buildCache {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return buildCache{}
	}
	var c buildCache
	if err := json.Unmarshal(data, &c); err != nil {
		return buildCache{}
	}
	return c
}

func saveCache(c buildCache) {
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	os.WriteFile(cachePath, data, 0644)
}

// --- hash-based change detection ---

// hashFiles computes a single SHA256 digest over the contents of all given files.
// Files are sorted by path before hashing so the result is order-independent.
func hashFiles(files []string) string {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
		h.Write([]byte{0})

		f, err := os.Open(f)
		if err != nil {
			continue
		}
		io.Copy(h, f)
		f.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func resHashChanged(c buildCache) bool {
	files := append(getFiles("res", ""), "AndroidManifest.xml")
	return hashFiles(files) != c.Res
}

func hashRes() string {
	files := append(getFiles("res", ""), "AndroidManifest.xml")
	return hashFiles(files)
}

func srcHashChanged(c buildCache) bool {
	files := append(getFiles("src", ""), getFiles("jar", "")...)
	return hashFiles(files) != c.Src
}

func hashSrc() string {
	files := append(getFiles("src", ""), getFiles("jar", "")...)
	return hashFiles(files)
}

func classesHashChanged(c buildCache) bool {
	files := getFiles(filepath.Join("build", "classes"), ".class")
	return hashFiles(files) != c.Classes
}

func hashClasses() string {
	files := getFiles(filepath.Join("build", "classes"), ".class")
	return hashFiles(files)
}

// --- time-based change detection ---

func getFileTimes(files []string) map[string]int64 {
	times := make(map[string]int64, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		times[f] = info.ModTime().UnixNano()
	}
	return times
}

func timesChanged(current, cached map[string]int64) bool {
	if len(current) != len(cached) {
		return true
	}
	for path, t := range current {
		if cached[path] != t {
			return true
		}
	}
	return false
}

func resTimeChanged(c buildCache) bool {
	files := append(getFiles("res", ""), "AndroidManifest.xml")
	return timesChanged(getFileTimes(files), c.ResTimes)
}

func timeRes() map[string]int64 {
	files := append(getFiles("res", ""), "AndroidManifest.xml")
	return getFileTimes(files)
}

func srcTimeChanged(c buildCache) bool {
	files := append(getFiles("src", ""), getFiles("jar", "")...)
	return timesChanged(getFileTimes(files), c.SrcTimes)
}

func timeSrc() map[string]int64 {
	files := append(getFiles("src", ""), getFiles("jar", "")...)
	return getFileTimes(files)
}

func classesTimeChanged(c buildCache) bool {
	files := getFiles(filepath.Join("build", "classes"), ".class")
	return timesChanged(getFileTimes(files), c.ClassTimes)
}

func timeClasses() map[string]int64 {
	files := getFiles(filepath.Join("build", "classes"), ".class")
	return getFileTimes(files)
}
