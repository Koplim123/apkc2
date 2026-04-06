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
	Encoding   string           `json:"encoding,omitempty"`
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

// --- file collectors ---

func collect(dirs ...string) []string {
	var out []string
	for _, d := range dirs {
		out = append(out, getFiles(d, "")...)
	}
	return out
}

func collectWithExt(dir, ext string) []string {
	return getFiles(dir, ext)
}

// --- hash-based change detection ---

func hashFiles(files []string) string {
	if len(files) == 0 {
		return ""
	}
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f))
		h.Write([]byte{0})

		fp, err := os.Open(f)
		if err != nil {
			continue
		}
		io.Copy(h, fp)
		fp.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func checkHash(cache buildCache, key string) (changed bool, hash string, times map[string]int64) {
	srcPaths := timesToPaths(cache.SrcTimes)
	times = getFileTimes(srcPaths)
	hash = hashFiles(srcPaths)
	return hash != cache.Src, hash, times
}

func timesToPaths(m map[string]int64) []string {
	p := make([]string, 0, len(m))
	for k := range m {
		p = append(p, k)
	}
	return p
}

func resHashChanged(c buildCache) bool {
	return hashFiles(collect("res")) != c.Res
}

func hashRes() string {
	return hashFiles(collect("res"))
}

func srcHashChanged(c buildCache) bool {
	return hashFiles(collect("src", "jar")) != c.Src
}

func hashSrc() string {
	return hashFiles(collect("src", "jar"))
}

func classesHashChanged(c buildCache) bool {
	return hashFiles(collectWithExt(filepath.Join("build", "classes"), ".class")) != c.Classes
}

func hashClasses() string {
	return hashFiles(collectWithExt(filepath.Join("build", "classes"), ".class"))
}

// --- time-based change detection ---

func getFileTimes(files []string) map[string]int64 {
	m := make(map[string]int64, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		m[f] = info.ModTime().UnixNano()
	}
	return m
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
	return timesChanged(getFileTimes(collect("res")), c.ResTimes)
}

func timeRes() map[string]int64 {
	return getFileTimes(collect("res"))
}

func srcTimeChanged(c buildCache) bool {
	return timesChanged(getFileTimes(collect("src", "jar")), c.SrcTimes)
}

func timeSrc() map[string]int64 {
	return getFileTimes(collect("src", "jar"))
}

func classesTimeChanged(c buildCache) bool {
	return timesChanged(getFileTimes(collectWithExt(filepath.Join("build", "classes"), ".class")), c.ClassTimes)
}

func timeClasses() map[string]int64 {
	return getFileTimes(collectWithExt(filepath.Join("build", "classes"), ".class"))
}
