package main

import (
	"archive/zip"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// build compiles all the source code and bundles into apk file with dependencies
func build() {
	buildCmd := flag.NewFlagSet("build", flag.ExitOnError)

	useAAB := buildCmd.Bool("aab", false, "build aab instead of apk")
	useUTF8 := buildCmd.Bool("utf8", false, "use UTF-8 source encoding for javac")
	useGBK := buildCmd.Bool("gbk", false, "use GBK source encoding for javac")
	useUTF8BOM := buildCmd.Bool("utf8wBOM", false, "use UTF-8 with BOM source encoding for javac")
	incremental := buildCmd.Bool("ic", false, "incremental build (time-based)")
	useHash := buildCmd.Bool("h", false, "use hash for incremental detection (requires -ic)")
	keyStore := buildCmd.String("keystore", keyStorePath, "path to keystore")
	storePass := buildCmd.String("storepass", "android", "keystore password")
	keyAlias := buildCmd.String("keyalias", "androiddebugkey", "key alias to use")
	sigAlg := buildCmd.String("sigalg", "SHA256withRSA", "signature to use")

	buildCmd.Parse(os.Args[2:])

	if *useHash && !*incremental {
		LogF("build", "-h requires -ic flag")
	}

	javaEncoding := resolveJavaEncoding(*useUTF8, *useGBK, *useUTF8BOM)

	prepare()

	if *incremental {
		if *useHash {
			LogI("build", "incremental build (hash mode)")
		} else {
			LogI("build", "incremental build (time mode)")
		}
		buildIncremental(*useAAB, *useHash, javaEncoding, keyStore, storePass, keyAlias, sigAlg)
	} else {
		buildFull(*useAAB, javaEncoding, keyStore, storePass, keyAlias, sigAlg)
	}
}

func resolveJavaEncoding(useUTF8, useGBK, useUTF8BOM bool) string {
	selected := 0
	if useUTF8 {
		selected++
	}
	if useGBK {
		selected++
	}
	if useUTF8BOM {
		selected++
	}
	if selected > 1 {
		LogF("build", "only one of --utf8, --gbk, --utf8wBOM can be specified")
	}
	if useGBK {
		return "GBK"
	}
	return "UTF-8"
}

func buildFull(useAAB bool, javaEncoding string, keyStore, storePass, keyAlias, sigAlg *string) {
	compileRes()
	bundleRes(useAAB)
	compileKotlin()
	compileJava(javaEncoding)
	bundleJava()
	if useAAB {
		buildBundle()
		buildAAB()
		signAAB(keyStore, storePass, keyAlias, sigAlg)
	} else {
		addDexToAPK(filepath.Join("build", "unaligned.apk"), filepath.Join("build", "classes.dex"))
		signAPK(keyStore, storePass, keyAlias)
	}
}

func buildIncremental(useAAB bool, useHash bool, javaEncoding string, keyStore, storePass, keyAlias, sigAlg *string) {
	cache := loadCache()

	var resUpdated, srcUpdated, clsUpdated bool
	if useHash {
		resUpdated = resHashChanged(cache)
		srcUpdated = srcHashChanged(cache)
		clsUpdated = classesHashChanged(cache)
	} else {
		resUpdated = resTimeChanged(cache)
		srcUpdated = srcTimeChanged(cache)
		clsUpdated = classesTimeChanged(cache)
	}

	if resUpdated {
		compileRes()
	} else {
		LogI("build", "skipping resource compilation (unchanged)")
	}
	bundleRes(useAAB)
	// bundleRes always regenerates src/R.java; refresh SrcTimes now so the
	// updated mtime is not mistaken for a source change on the next run.
	if !useHash {
		cache.SrcTimes = timeSrc()
	}
	if resUpdated {
		if useHash {
			cache.Res = hashRes()
		}
		cache.ResTimes = timeRes()
	}
	saveCache(cache)

	if srcUpdated {
		compileKotlin()
		compileJava(javaEncoding)
		if useHash {
			cache.Src = hashSrc()
		}
		cache.SrcTimes = timeSrc()
		saveCache(cache)
	} else {
		LogI("build", "skipping java/kotlin compilation (unchanged)")
	}

	if srcUpdated || clsUpdated {
		bundleJava()
		if useHash {
			cache.Classes = hashClasses()
		}
		cache.ClassTimes = timeClasses()
		saveCache(cache)
	} else {
		LogI("build", "skipping dex bundling (unchanged)")
	}

	if useAAB {
		buildBundle()
		buildAAB()
		signAAB(keyStore, storePass, keyAlias, sigAlg)
	} else {
		addDexToAPK(filepath.Join("build", "unaligned.apk"), filepath.Join("build", "classes.dex"))
		signAPK(keyStore, storePass, keyAlias)
	}
}

// clean simply deletes the build dir
func clean() {
	LogI("clean", "removing build/*")
	os.RemoveAll("build")
}

// prepare verifies the project paths and setup the build dir
func prepare() {
	mustExist := func(path string) {
		if _, err := os.Stat(path); err != nil {
			LogF("build", err)
		}
	}

	mustExist("src")
	mustExist("res")
	mustExist("AndroidManifest.xml")

	mustMkdir := func(path string) {
		// only ignore if error is "already exist"
		if err := os.MkdirAll(path, 0755); err != nil && !os.IsExist(err) {
			LogF("build", err)
		}
	}

	mustMkdir(filepath.Join("build", "flats"))
	mustMkdir(filepath.Join("build", "classes"))
}

// compileRes compiles the xml files in res dir
func compileRes() {
	res := getFiles("res", "")
	LogI("build", "compiling resources")
	args := []string{"compile", "-o", filepath.Join("build", "flats")}
	args = append(args, res...)
	cmd := exec.Command(aapt2Path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func bundleRes(useAAB bool) {
	LogI("build", "bundling resources")

	flats := getFiles("build/flats", ".flat")
	args := []string{"link", "-I", androidJar, "--manifest", "AndroidManifest.xml", "--java", "src"}
	if useAAB {
		args = append(args, "-o", "build", "--output-to-dir", "--proto-format")
	} else {
		args = append(args, "-o", filepath.Join("build", "unaligned.apk"))
	}
	args = append(args, flats...)
	cmd := exec.Command(aapt2Path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func compileKotlin() {
	kotlins := getFiles("src", "kt")
	if len(kotlins) < 1 {
		return
	}

	LogI("build", "compiling kotlin files")

	jars := strings.Join(getFiles("jar", "jar"), string(os.PathListSeparator))

	classpathParts := []string{androidJar}
	if jars != "" {
		classpathParts = append(classpathParts, jars)
	}
	classpath := strings.Join(classpathParts, string(os.PathListSeparator))

	args := []string{"-jvm-target", "1.8", "-d", filepath.Join("build", "classes"), "-classpath", classpath, "src"}
	args = append(args, kotlins...)
	cmd := exec.Command(kotlincPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out), args)
	}
}

// compileJava compiles java files in src dir and uses jar files in the jar dir as classpath
func compileJava(encoding string) {
	javas := getFiles("src", "java")
	if len(javas) == 0 {
		LogF("build", "no java files found in src")
	}

	LogI("build", "compiling java files")

	classpathParts := []string{androidJar, filepath.Join("build", "classes"), "src"}
	if jarFiles := getFiles("jar", "jar"); len(jarFiles) > 0 {
		classpathParts = append(classpathParts, strings.Join(jarFiles, string(os.PathListSeparator)))
	}
	classpath := strings.Join(classpathParts, string(os.PathListSeparator))

	args := []string{"-encoding", encoding, "-source", "8", "-target", "8", "-d", filepath.Join("build", "classes"), "-classpath", classpath}
	args = append(args, javas...)

	cmd := exec.Command(javacPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

// bundleJava bundles compiled java class files and external jar files into apk
func bundleJava() {
	LogI("build", "bundling classes and jars")

	classes := getFiles(filepath.Join("build", "classes"), ".class")
	jars := getFiles("jar", ".jar")

	args := []string{"--lib", androidJar, "--release", "--output", "build"}
	args = append(args, classes...)
	args = append(args, jars...)

	var cmd *exec.Cmd
	lowerD8Path := strings.ToLower(d8Path)
	if strings.HasSuffix(lowerD8Path, ".bat") || strings.HasSuffix(lowerD8Path, ".cmd") {
		javaExec := "java"
		if javaBinPath != "" {
			javaExec = filepath.Join(javaBinPath, "java.exe")
		}

		d8Jar := filepath.Join(filepath.Dir(d8Path), "lib", "d8.jar")
		cmdArgs := []string{"-cp", d8Jar, "com.android.tools.r8.D8"}
		cmdArgs = append(cmdArgs, args...)
		cmd = exec.Command(javaExec, cmdArgs...)
	} else {
		cmd = exec.Command(d8Path, args...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func buildBundle() {
	outFile, err := os.Create(filepath.Join("build", "bundle.zip"))
	if err != nil {
		LogF("build", err)
	}
	defer outFile.Close()

	w := zip.NewWriter(outFile)

	addFileToZip := func(s, d string, compress bool) {
		var dst io.Writer
		if compress {
			dst, err = w.Create(d)
		} else {
			dst, err = w.CreateHeader(&zip.FileHeader{
				Name:   d,
				Method: zip.Store,
			})
		}
		if err != nil {
			LogF("build", err)
		}

		src, err := os.Open(s)
		if err != nil {
			LogF("build", err)
		}

		_, err = io.Copy(dst, src)
		if err != nil {
			LogF("build", err)
		}
	}

	addFileToZip(filepath.Join("build", "AndroidManifest.xml"), filepath.Join("manifest", "AndroidManifest.xml"), true)
	addFileToZip(filepath.Join("build", "classes.dex"), filepath.Join("dex", "classes.dex"), true)
	addFileToZip(filepath.Join("build", "resources.pb"), "resources.pb", true)

	files := getFiles(filepath.Join("build", "res"), "")
	for _, f := range files {
		r, err := filepath.Rel("build", f)
		if err != nil {
			LogF("build", err)
		}
		addFileToZip(f, r, true)
	}

	files = getFiles("assets", "")
	if len(files) > 0 {
		LogI("build", "bundling assets")
	}
	for _, f := range files {
		addFileToZip(f, f, true)
	}

	files = getFiles("lib", "")
	if len(files) > 0 {
		LogI("build", "bundling native libs")
	}
	for _, f := range files {
		addFileToZip(f, f, true)
	}

	err = w.Close()
	if err != nil {
		LogF("build", err)
	}
}
