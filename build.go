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

type buildOpts struct {
	useAAB       bool
	incremental  bool
	useHash      bool
	javaEncoding string
	keyStore     *string
	storePass    *string
	keyAlias     *string
	sigAlg       *string
}

// build compiles all source code and bundles into an apk/aab file.
func build() {
	cmd := flag.NewFlagSet("build", flag.ExitOnError)

	useAAB := cmd.Bool("aab", false, "build aab instead of apk")
	incremental := cmd.Bool("ic", false, "incremental build (time-based)")
	useHash := cmd.Bool("h", false, "use hash for incremental detection (requires -ic)")
	keyStore := cmd.String("keystore", keyStorePath, "path to keystore")
	storePass := cmd.String("storepass", "android", "keystore password")
	keyAlias := cmd.String("keyalias", "androiddebugkey", "key alias to use")
	sigAlg := cmd.String("sigalg", "SHA256withRSA", "signature algorithm")
	encUTF8 := cmd.Bool("utf8", false, "use UTF-8 source encoding for javac")
	encGBK := cmd.Bool("gbk", false, "use GBK source encoding for javac")
	encUTF8BOM := cmd.Bool("utf8bom", false, "use UTF-8 with BOM source encoding for javac")

	cmd.Parse(os.Args[2:])

	opts := buildOpts{
		useAAB:      *useAAB,
		incremental: *incremental,
		useHash:     *useHash,
		keyStore:    keyStore,
		storePass:   storePass,
		keyAlias:    keyAlias,
		sigAlg:      sigAlg,
	}

	if *encUTF8 && *encGBK || *encUTF8 && *encUTF8BOM || *encGBK && *encUTF8BOM {
		LogF("build", "only one encoding flag (--utf8, --gbk, --utf8bom) may be specified")
	}
	if opts.useHash && !opts.incremental {
		LogF("build", "-h requires -ic flag")
	}

	switch {
	case *encGBK:
		opts.javaEncoding = "GBK"
	case *encUTF8, *encUTF8BOM:
		opts.javaEncoding = "UTF-8"
	default:
		opts.javaEncoding = "UTF-8"
	}

	prepare()

	if opts.incremental {
		if opts.useHash {
			LogI("build", "incremental build (hash mode)")
		} else {
			LogI("build", "incremental build (time mode)")
		}
		buildIncremental(opts)
	} else {
		buildFull(opts)
	}
}

func buildFull(opts buildOpts) {
	compileRes()
	bundleRes(opts.useAAB)
	compileKotlin()
	compileJava(opts.javaEncoding)
	bundleJava()
	pack(opts)
}

func buildIncremental(opts buildOpts) {
	cache := loadCache()

	if opts.javaEncoding != cache.Encoding {
		// Encoding changed: invalidate all source and class artifacts.
		cache.Src = ""
		cache.Classes = ""
		cache.SrcTimes = nil
		cache.ClassTimes = nil
		cache.Encoding = opts.javaEncoding
		saveCache(cache)
	}

	var resUpdated, srcUpdated, clsUpdated bool
	if opts.useHash {
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
	bundleRes(opts.useAAB)

	// bundleRes always regenerates src/R.java; snapshot SrcTimes immediately
	// so the new mtime is not misread as a source change on the next run.
	cache.SrcTimes = timeSrc()
	if resUpdated {
		if opts.useHash {
			cache.Res = hashRes()
		}
		cache.ResTimes = timeRes()
	}
	saveCache(cache)

	if srcUpdated {
		compileKotlin()
		compileJava(opts.javaEncoding)
		if opts.useHash {
			cache.Src = hashSrc()
		}
		cache.SrcTimes = timeSrc()
		saveCache(cache)
	} else {
		LogI("build", "skipping java/kotlin compilation (unchanged)")
	}

	if srcUpdated || clsUpdated {
		bundleJava()
		if opts.useHash {
			cache.Classes = hashClasses()
		}
		cache.ClassTimes = timeClasses()
		saveCache(cache)
	} else {
		LogI("build", "skipping dex bundling (unchanged)")
	}

	pack(opts)
}

func pack(opts buildOpts) {
	if opts.useAAB {
		buildBundle()
		buildAAB()
		signAAB(opts.keyStore, opts.storePass, opts.keyAlias, opts.sigAlg)
	} else {
		addDexToAPK(filepath.Join("build", "unaligned.apk"), filepath.Join("build", "classes.dex"))
		signAPK(opts.keyStore, opts.storePass, opts.keyAlias)
	}
}

// clean simply deletes the build dir.
func clean() {
	LogI("clean", "removing build/*")
	os.RemoveAll("build")
}

// prepare verifies the project layout and sets up the build directory.
func prepare() {
	for _, path := range []string{"src", "res", "AndroidManifest.xml"} {
		if _, err := os.Stat(path); err != nil {
			LogF("build", err)
		}
	}
	for _, dir := range []string{filepath.Join("build", "flats"), filepath.Join("build", "classes")} {
		if err := os.MkdirAll(dir, 0755); err != nil && !os.IsExist(err) {
			LogF("build", err)
		}
	}
}

func compileRes() {
	LogI("build", "compiling resources")
	args := []string{"compile", "-o", filepath.Join("build", "flats")}
	args = append(args, getFiles("res", "")...)
	cmd := exec.Command(aapt2Path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func bundleRes(useAAB bool) {
	LogI("build", "bundling resources")
	args := []string{"link", "-I", androidJar, "--manifest", "AndroidManifest.xml", "--java", "src"}
	if useAAB {
		args = append(args, "-o", "build", "--output-to-dir", "--proto-format")
	} else {
		args = append(args, "-o", filepath.Join("build", "unaligned.apk"))
	}
	args = append(args, getFiles("build/flats", ".flat")...)
	cmd := exec.Command(aapt2Path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func compileKotlin() {
	ktFiles := getFiles("src", "kt")
	if len(ktFiles) == 0 {
		return
	}

	LogI("build", "compiling kotlin files")
	cp := androidJar
	if jars := getFiles("jar", "jar"); len(jars) > 0 {
		cp = strings.Join(append([]string{androidJar}, jars...), string(os.PathListSeparator))
	}

	args := []string{"-jvm-target", "1.8", "-d", filepath.Join("build", "classes"), "-classpath", cp, "src"}
	args = append(args, ktFiles...)
	cmd := exec.Command(kotlincPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out), args)
	}
}

func compileJava(encoding string) {
	javas := getFiles("src", "java")
	if len(javas) == 0 {
		LogF("build", "no java files found in src")
	}

	LogI("build", "compiling java files")
	classpath := androidJar + string(os.PathListSeparator) +
		filepath.Join("build", "classes") + string(os.PathListSeparator) + "src"
	if jars := getFiles("jar", "jar"); len(jars) > 0 {
		classpath += string(os.PathListSeparator) + strings.Join(jars, string(os.PathListSeparator))
	}

	args := []string{
		"-encoding", encoding,
		"-source", "8", "-target", "8",
		"-d", filepath.Join("build", "classes"),
		"-classpath", classpath,
	}
	args = append(args, javas...)
	cmd := exec.Command(javacPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		LogF("build", string(out))
	}
}

func bundleJava() {
	LogI("build", "bundling classes and jars")
	classes := getFiles(filepath.Join("build", "classes"), ".class")
	jars := getFiles("jar", ".jar")

	args := []string{"--lib", androidJar, "--release", "--output", "build"}
	args = append(args, classes...)
	args = append(args, jars...)

	var cmd *exec.Cmd
	lowerD8 := strings.ToLower(d8Path)
	if strings.HasSuffix(lowerD8, ".bat") || strings.HasSuffix(lowerD8, ".cmd") {
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
	defer func() {
		if cerr := outFile.Close(); cerr != nil && err == nil {
			LogF("build", cerr)
		}
	}()

	w := zip.NewWriter(outFile)
	defer w.Close()

	addFileToZip := func(srcPath, zipPath string, compress bool) {
		dst, zerr := w.Create(zipPath)
		if !compress || zerr != nil {
			dst, zerr = w.CreateHeader(&zip.FileHeader{
				Name:   zipPath,
				Method: zip.Store,
			})
		}
		if zerr != nil {
			LogF("build", zerr)
		}

		src, serr := os.Open(srcPath)
		if serr != nil {
			LogF("build", serr)
		}
		if _, cerr := io.Copy(dst, src); cerr != nil {
			src.Close()
			LogF("build", cerr)
		}
		src.Close()
	}

	addFileToZip(filepath.Join("build", "AndroidManifest.xml"), filepath.Join("manifest", "AndroidManifest.xml"), true)
	addFileToZip(filepath.Join("build", "classes.dex"), filepath.Join("dex", "classes.dex"), true)
	addFileToZip(filepath.Join("build", "resources.pb"), "resources.pb", true)

	for _, f := range getFiles(filepath.Join("build", "res"), "") {
		rel, err := filepath.Rel("build", f)
		if err != nil {
			LogF("build", err)
		}
		addFileToZip(f, rel, true)
	}

	if assets := getFiles("assets", ""); len(assets) > 0 {
		LogI("build", "bundling assets")
		for _, f := range assets {
			addFileToZip(f, f, true)
		}
	}

	if libs := getFiles("lib", ""); len(libs) > 0 {
		LogI("build", "bundling native libs")
		for _, f := range libs {
			addFileToZip(f, f, true)
		}
	}
}
