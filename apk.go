package main

import (
	"archive/zip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// addDexToAPK appends classes.dex into an existing APK (zip) file
func addDexToAPK(apkPath, dexPath string) {
	LogI("build", "adding dex to apk")

	// open existing apk for appending
	f, err := os.OpenFile(apkPath, os.O_RDWR, 0644)
	if err != nil {
		LogF("build", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		LogF("build", err)
	}

	w := zip.NewWriter(f)

	// copy existing zip entries
	r, err := zip.NewReader(f, fi.Size())
	if err != nil {
		LogF("build", err)
	}
	for _, entry := range r.File {
		w.Copy(entry)
	}

	// append classes.dex
	dex, err := os.Open(dexPath)
	if err != nil {
		LogF("build", err)
	}
	defer dex.Close()

	fw, err := w.Create("classes.dex")
	if err != nil {
		LogF("build", err)
	}
	if _, err := io.Copy(fw, dex); err != nil {
		LogF("build", err)
	}

	if err := w.Close(); err != nil {
		LogF("build", err)
	}
}

// signAPK signs apk with apksigner and provided debug keys
func signAPK(keyStore, storePass, keyAlias *string) {
	LogI("build", "signing app")

	apkPath := filepath.Join("build", "unaligned.apk")
	if _, err := os.Stat(apkPath); err != nil {
		LogF("build", err)
	}

	cmd := exec.Command(apksignerPath, "sign", "--ks-pass", "pass:"+*storePass, "--ks", *keyStore, "--ks-key-alias", *keyAlias, apkPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}

	if err := os.Rename(apkPath, filepath.Join("build", "app.apk")); err != nil {
		LogF("build", err)
	}
}
