//go:build linux || darwin

package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func objectTestFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestS3CredentialsOpen(t *testing.T) {
	path := objectTestFile(t, "credentials", []byte("[default]\naws_access_key_id = synthetic-id\naws_secret_access_key = synthetic-secret\n"))
	if _, err := readS3CredentialsFile(path); err != nil {
		t.Fatal(err)
	}
	ops := defaultObjectFileOps()
	ops.private = func(*os.File) bool { return false }
	if _, err := readS3CredentialsFileWithOps(path, ops); !errors.Is(err, vngcloud.ErrCredentialsFile) {
		t.Fatalf("owner check: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil { // #nosec G302 -- Unsafe mode verifies credential rejection.
		t.Fatal(err)
	}
	for _, bad := range []string{path, link, fifo, t.TempDir(), "/dev/null", filepath.Join(t.TempDir(), "missing")} {
		done := make(chan error, 1)
		go func() { _, err := readS3CredentialsFile(bad); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, vngcloud.ErrCredentialsFile) {
				t.Fatalf("unsafe file accepted: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("credential open blocked")
		}
	}
}

func TestObjectSnapshot(t *testing.T) {
	if storage.MaxPutObjectSize != 5_000_000_000 {
		t.Fatal("wrong cap")
	}
	for _, data := range [][]byte{nil, {0, 255, 1, '\n'}} {
		path := objectTestFile(t, "source", data)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		f, cleanup, err := snapshotObjectFile(context.Background(), link)
		if err != nil {
			t.Fatal(err)
		}
		name := f.Name()
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		dir, err := os.Stat(filepath.Dir(name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f)
		if err != nil || !bytes.Equal(got, data) || info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 || !strings.HasPrefix(filepath.Base(filepath.Dir(name)), "vngcloud-object-") {
			t.Fatal("invalid snapshot")
		}
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Dir(name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("snapshot remains")
		}
	}
	path := objectTestFile(t, "source", []byte("12345"))
	f, _, err := snapshotObjectFileWithOps(context.Background(), path, 4, defaultObjectFileOps())
	if err == nil || f != nil {
		t.Fatal("oversize snapshot accepted")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"-", "file://source", fifo, "/dev/null", t.TempDir()} {
		if f, _, err := snapshotObjectFile(context.Background(), path); err == nil || f != nil {
			t.Fatal("unsafe source accepted")
		}
	}
}

type objectTestBody struct {
	io.Reader
	closed   bool
	closeErr error
}

func (b *objectTestBody) Close() error { b.closed = true; return b.closeErr }

func TestObjectDownloadPublish(t *testing.T) {
	for _, data := range [][]byte{nil, {0, 255, 'x'}} {
		for _, replace := range []bool{false, true} {
			destination := filepath.Join(t.TempDir(), "result")
			if replace {
				if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			body := &objectTestBody{Reader: bytes.NewReader(data)}
			result, err := downloadObjectFile(context.Background(), body, destination, replace)
			sum := sha256.Sum256(data)
			if err != nil || !result.Published || result.ContentLength != int64(len(data)) || result.SHA256 != hex.EncodeToString(sum[:]) || !body.closed {
				t.Fatalf("result = %+v, %v", result, err)
			}
			got, err := os.ReadFile(destination)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("wrong download")
			}
			info, err := os.Stat(destination)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("wrong mode")
			}
		}
	}
	for _, kind := range []string{"file", "directory", "symlink", "dangling"} {
		destination := filepath.Join(t.TempDir(), "result")
		switch kind {
		case "file":
			if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
		case "directory":
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
		default:
			target := objectTestFile(t, "target", []byte("old"))
			if kind == "dangling" {
				target += "-missing"
			}
			if err := os.Symlink(target, destination); err != nil {
				t.Fatal(err)
			}
		}
		for _, replace := range []bool{false, true} {
			if kind == "file" && replace {
				continue
			}
			body := &objectTestBody{Reader: strings.NewReader("new")}
			result, err := downloadObjectFile(context.Background(), body, destination, replace)
			if err == nil || result.Published || !body.closed {
				t.Fatal("occupied destination accepted")
			}
		}
	}
}

func TestObjectDownloadFaults(t *testing.T) {
	fault := errors.New("hidden body and secret")
	for _, phase := range []string{"create", "copy", "sync", "close", "link", "rename", "body-close", "cancel", "competitor", "type-change"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "result")
			replace := phase == "rename" || phase == "type-change"
			if replace {
				if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ops := defaultObjectFileOps()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "create":
				ops.create = func(string, string) (*os.File, error) { return nil, fault }
			case "copy":
				ops.copy = func(context.Context, io.Writer, io.Reader, int64) (int64, error) { return 0, fault }
			case "sync":
				ops.sync = func(*os.File) error { return fault }
			case "close":
				ops.close = func(f *os.File) error { _ = f.Close(); return fault }
			case "link":
				ops.link = func(string, string) error { return fault }
			case "rename":
				ops.rename = func(string, string) error { return fault }
			case "cancel":
				original := ops.sync
				ops.sync = func(f *os.File) error { cancel(); return original(f) }
			case "competitor":
				original := ops.link
				ops.link = func(a, b string) error {
					if err := os.WriteFile(b, []byte("competitor"), 0o600); err != nil {
						return err
					}
					return original(a, b)
				}
			case "type-change":
				original := ops.close
				ops.close = func(f *os.File) error {
					if err := os.Remove(destination); err != nil {
						return err
					}
					if err := os.Symlink("missing", destination); err != nil {
						return err
					}
					return original(f)
				}
			}
			body := &objectTestBody{Reader: strings.NewReader("new")}
			if phase == "body-close" {
				body.closeErr = fault
			}
			result, err := downloadObjectFileWithOps(ctx, body, destination, replace, ops)
			if err == nil || result.Published || !body.closed || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("unsafe failure: %+v %v", result, err)
			}
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".vngcloud-object-") {
					t.Fatal("partial temp remains")
				}
			}
			if phase == "rename" {
				got, err := os.ReadFile(destination)
				if err != nil || string(got) != "old" {
					t.Fatal("old file lost")
				}
			}
			if phase == "competitor" {
				got, err := os.ReadFile(destination)
				if err != nil || string(got) != "competitor" {
					t.Fatal("competitor clobbered")
				}
			}
		})
	}
}

func TestObjectDownloadPublishedCleanup(t *testing.T) {
	for _, replace := range []bool{false, true} {
		for _, phase := range []string{"remove", "directory-sync"} {
			if replace && phase == "remove" {
				continue
			}
			t.Run(phase, func(t *testing.T) {
				dir := t.TempDir()
				destination := filepath.Join(dir, "result")
				if replace {
					if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ops := defaultObjectFileOps()
				fault := errors.New("hidden")
				if phase == "remove" {
					ops.remove = func(string) error { return fault }
				} else {
					ops.syncDir = func(string) error { return fault }
				}
				body := &objectTestBody{Reader: strings.NewReader("finished")}
				result, err := downloadObjectFileWithOps(context.Background(), body, destination, replace, ops)
				if err == nil || !result.Published || !body.closed || !strings.Contains(err.Error(), "download was published") || strings.Contains(err.Error(), "hidden") {
					t.Fatalf("lost completed state: %+v %v", result, err)
				}
				got, err := os.ReadFile(destination)
				if err != nil || string(got) != "finished" {
					t.Fatal("published file lost")
				}
				sum := sha256.Sum256(got)
				if result.ContentLength != int64(len(got)) || result.SHA256 != hex.EncodeToString(sum[:]) {
					t.Fatal("cleanup failure lost completed metadata")
				}
				entries, err := os.ReadDir(dir)
				wantEntries := 1
				if phase == "remove" {
					wantEntries = 2
				}
				if err != nil || len(entries) != wantEntries {
					t.Fatal("unexpected published cleanup state")
				}
			})
		}
	}
}

func TestObjectSnapshotFaults(t *testing.T) {
	fault := errors.New("hidden object bytes")
	for _, phase := range []string{"open", "stat", "mkdir", "create", "copy", "source-close", "sync", "seek", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			path := objectTestFile(t, "source", []byte("original"))
			ops := defaultObjectFileOps()
			var tempDir string
			mkdir := ops.mkdirTemp
			ops.mkdirTemp = func(parent, pattern string) (string, error) {
				var err error
				tempDir, err = mkdir(parent, pattern)
				return tempDir, err
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch phase {
			case "open":
				ops.open = func(string, bool) (*os.File, error) { return nil, fault }
			case "stat":
				ops.stat = func(*os.File) (os.FileInfo, error) { return nil, fault }
			case "mkdir":
				ops.mkdirTemp = func(string, string) (string, error) { return "", fault }
			case "create":
				ops.create = func(string, string) (*os.File, error) { return nil, fault }
			case "copy":
				ops.copy = func(_ context.Context, dst io.Writer, _ io.Reader, _ int64) (int64, error) {
					_, err := dst.Write([]byte("partial"))
					if err != nil {
						return 0, err
					}
					return 7, fault
				}
			case "source-close":
				closeFile := ops.close
				ops.close = func(f *os.File) error {
					err := closeFile(f)
					if f.Name() == path {
						return fault
					}
					return err
				}
			case "sync":
				ops.sync = func(*os.File) error { return fault }
			case "seek":
				ops.seek = func(*os.File) error { return fault }
			case "cancel":
				original := ops.copy
				ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
					cancel()
					return original(ctx, dst, src, limit)
				}
			}
			f, cleanup, err := snapshotObjectFileWithOps(ctx, path, 32, ops)
			if err == nil || f != nil || cleanup != nil || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("unsafe snapshot failure: %v", err)
			}
			if phase == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation")
			}
			if tempDir != "" {
				if _, err := os.Stat(tempDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("private temp remains")
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "original" {
				t.Fatal("source changed")
			}
		})
	}
}

func TestObjectSnapshotBoundedCopy(t *testing.T) {
	path := objectTestFile(t, "source", bytes.Repeat([]byte{1}, 20))
	for _, size := range []int64{0, 4, 5} {
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
		ops := defaultObjectFileOps()
		original := ops.copy
		var count int64
		ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
			var err error
			count, err = original(ctx, dst, src, limit)
			return count, err
		}
		f, cleanup, err := snapshotObjectFileWithOps(context.Background(), path, 4, ops)
		if count != size {
			t.Fatalf("copied %d, want %d", count, size)
		}
		if size > 4 {
			if err == nil || f != nil {
				t.Fatal("probe byte accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObjectSnapshotSourceChange(t *testing.T) {
	path := objectTestFile(t, "source", []byte("before"))
	ops := defaultObjectFileOps()
	original := ops.copy
	ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
		if err := os.WriteFile(path, []byte("during"), 0o600); err != nil {
			return 0, err
		}
		return original(ctx, dst, src, limit)
	}
	f, cleanup, err := snapshotObjectFileWithOps(context.Background(), path, 32, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after!"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(f)
	if err != nil || string(got) != "during" {
		t.Fatal("snapshot depends on later source changes")
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

type objectFaultReader struct{ err error }

func (r objectFaultReader) Read(p []byte) (int, error) { p[0] = 'x'; return 1, r.err }

func TestObjectDownloadBodyFailures(t *testing.T) {
	fault := errors.New("hidden object bytes and secret")
	for name, tc := range map[string]struct {
		err error
		raw bool
	}{
		"short": {io.ErrUnexpectedEOF, false}, "overflow": {errors.New("hidden oversized body"), true}, "canceled": {context.Canceled, false}, "deadline": {context.DeadlineExceeded, false},
		"wrapped-EOF": {fmt.Errorf("hidden: %w", io.EOF), true}, "joined-EOF": {errors.Join(io.EOF, fault), true},
		"joined-canceled": {errors.Join(io.EOF, context.Canceled, fault), true}, "joined-deadline": {errors.Join(io.EOF, context.DeadlineExceeded, fault), true}, "joined-short": {errors.Join(io.EOF, io.ErrUnexpectedEOF, fault), true},
	} {
		t.Run(name, func(t *testing.T) {
			destination := objectTestFile(t, "result", []byte("old"))
			body := &objectTestBody{Reader: objectFaultReader{err: tc.err}}
			result, err := downloadObjectFile(context.Background(), body, destination, true)
			if err == nil || result.Published || !body.closed || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("unsafe body failure: %v", err)
			}
			for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
				if errors.Is(tc.err, sentinel) && !errors.Is(err, sentinel) {
					t.Fatal("lost safe sentinel")
				}
			}
			if tc.raw && errors.Is(err, tc.err) {
				t.Fatal("retained raw reader error")
			}
			got, err := os.ReadFile(destination)
			if err != nil || string(got) != "old" {
				t.Fatal("partial body published")
			}
		})
	}
}

func TestObjectDownloadCleanEOF(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "result")
	ops := defaultObjectFileOps()
	var events []string
	originalSync, originalClose, originalLink := ops.sync, ops.close, ops.link
	ops.sync = func(f *os.File) error { events = append(events, "sync"); return originalSync(f) }
	ops.close = func(f *os.File) error { events = append(events, "close"); return originalClose(f) }
	ops.link = func(a, b string) error { events = append(events, "link"); return originalLink(a, b) }
	body := &objectTestBody{Reader: objectEOFReader{events: &events}}
	result, err := downloadObjectFileWithOps(context.Background(), body, destination, false, ops)
	if err != nil || !result.Published || strings.Join(events, ",") != "read,eof,sync,close,link" {
		t.Fatalf("publication order %v, %v", events, err)
	}
}

type objectEOFReader struct{ events *[]string }

func (r objectEOFReader) Read(p []byte) (int, error) {
	if len(*r.events) == 0 {
		*r.events = append(*r.events, "read")
		p[0] = 'x'
		return 1, nil
	}
	*r.events = append(*r.events, "eof")
	return 0, io.EOF
}

func TestS3CredentialsSwap(t *testing.T) {
	path := objectTestFile(t, "credentials", []byte("[default]\naws_access_key_id = synthetic-id\naws_secret_access_key = synthetic-secret\n"))
	target := objectTestFile(t, "target", []byte("hidden"))
	ops := defaultObjectFileOps()
	original := ops.open
	ops.open = func(path string, credentials bool) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := os.Symlink(target, path); err != nil {
			return nil, err
		}
		return original(path, credentials)
	}
	if _, err := readS3CredentialsFileWithOps(path, ops); !errors.Is(err, vngcloud.ErrCredentialsFile) || strings.Contains(err.Error(), "hidden") {
		t.Fatalf("symlink swap accepted: %v", err)
	}
}

func TestS3CredentialsFileFaults(t *testing.T) {
	for _, phase := range []string{"open", "stat", "privacy", "read", "close-before", "close-after"} {
		t.Run(phase, func(t *testing.T) {
			content := []byte("[default]\naws_access_key_id = synthetic-id\naws_secret_access_key = synthetic-secret\n")
			path := objectTestFile(t, "hidden-credential-reader", content)
			ops := defaultObjectFileOps()
			fault := errors.New("hidden synthetic-id synthetic-secret")
			var opened *os.File
			open := ops.open
			ops.open = func(path string, credentials bool) (*os.File, error) {
				var err error
				opened, err = open(path, credentials)
				return opened, err
			}
			closeCalls := 0
			closeFile := ops.close
			ops.close = func(f *os.File) error {
				closeCalls++
				if phase == "close-before" {
					return fault
				}
				err := closeFile(f)
				if phase == "close-after" {
					return fault
				}
				return err
			}
			switch phase {
			case "open":
				ops.open = func(string, bool) (*os.File, error) { return nil, fault }
			case "stat":
				ops.stat = func(*os.File) (os.FileInfo, error) { return nil, fault }
			case "privacy":
				ops.private = func(*os.File) bool { return false }
			case "read":
				private := ops.private
				ops.private = func(f *os.File) bool {
					if !private(f) {
						t.Fatal("fixture is not private")
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
					return true
				}
			}
			credentials, err := readS3CredentialsFileWithOps(path, ops)
			if !errors.Is(err, vngcloud.ErrCredentialsFile) || credentials.AccessKeyID.Reveal() != "" || credentials.SecretAccessKey.Reveal() != "" {
				t.Fatalf("credential failure returned values: %v", err)
			}
			if strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "synthetic") || errors.Is(err, fault) || errors.Is(err, os.ErrClosed) {
				t.Fatal("credential failure exposed raw cause")
			}
			wantCloseCalls := 0
			if strings.HasPrefix(phase, "close-") {
				wantCloseCalls = 1
			}
			if closeCalls != wantCloseCalls {
				t.Fatalf("close calls = %d, want %d", closeCalls, wantCloseCalls)
			}
			if opened != nil {
				if _, err := opened.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("credential handle remains open")
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, content) {
				t.Fatal("credential file changed")
			}
		})
	}
}

func TestS3CredentialsFileSize(t *testing.T) {
	base := "[default]\naws_access_key_id = synthetic-id\naws_secret_access_key = synthetic-secret\n"
	for _, size := range []int{maxS3CredentialsSize, maxS3CredentialsSize + 1} {
		data := base + "#" + strings.Repeat("x", size-len(base)-1)
		path := objectTestFile(t, "credentials", []byte(data))
		credentials, err := readS3CredentialsFile(path)
		if size == maxS3CredentialsSize {
			if err != nil || credentials.AccessKeyID.Reveal() != "synthetic-id" || credentials.SecretAccessKey.Reveal() != "synthetic-secret" {
				t.Fatal("exact credential limit rejected")
			}
		} else if !errors.Is(err, vngcloud.ErrCredentialsFile) || credentials.AccessKeyID.Reveal() != "" || credentials.SecretAccessKey.Reveal() != "" {
			t.Fatal("oversize credential file accepted")
		}
	}
}

func TestObjectFilesDestinationCheck(t *testing.T) {
	for _, kind := range []string{"missing", "file", "directory", "symlink", "dangling", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "destination")
			target := objectTestFile(t, "target", []byte("original"))
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling":
				linkTarget := target
				if kind == "dangling" {
					linkTarget += "-missing"
				}
				if err := os.Symlink(linkTarget, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, replace := range []bool{false, true} {
				err := checkObjectDestination(path, replace)
				allowed := kind == "missing" || (kind == "file" && replace)
				if (err == nil) != allowed {
					t.Fatalf("replace %t: %v", replace, err)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "missing" {
				want = 0
			}
			if len(entries) != want {
				t.Fatal("destination check changed directory entries")
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "original" {
				t.Fatal("destination check changed target")
			}
			if kind == "file" {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "original" {
					t.Fatal("destination check changed file")
				}
			}
		})
	}
	parentFile := objectTestFile(t, "parent", nil)
	for _, path := range []string{"", "-", "file://destination", filepath.Join(t.TempDir(), "missing", "destination"), filepath.Join(parentFile, "destination")} {
		for _, replace := range []bool{false, true} {
			if err := checkObjectDestination(path, replace); err == nil {
				t.Fatal("invalid destination accepted")
			}
		}
	}
}

func TestObjectSnapshotCleanupFaults(t *testing.T) {
	for _, phase := range []string{"close-before", "close-after", "remove-file", "remove-directory", "foreign-entry"} {
		t.Run(phase, func(t *testing.T) {
			path := objectTestFile(t, "source", []byte("original"))
			ops := defaultObjectFileOps()
			parent := t.TempDir()
			ops.mkdirTemp = func(string, string) (string, error) { return os.MkdirTemp(parent, "vngcloud-object-*") }
			fault := errors.New("hidden object bytes and secret")
			closeFile := ops.close
			ops.close = func(f *os.File) error {
				if f.Name() != path && phase == "close-before" {
					return fault
				}
				err := closeFile(f)
				if f.Name() != path && phase == "close-after" {
					return fault
				}
				return err
			}
			var removed []string
			remove := ops.remove
			ops.remove = func(name string) error {
				removed = append(removed, name)
				if (phase == "remove-file" && filepath.Dir(name) != parent) || (phase == "remove-directory" && filepath.Dir(name) == parent) {
					return fault
				}
				return remove(name)
			}
			f, cleanup, err := snapshotObjectFileWithOps(context.Background(), path, 32, ops)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.Close() })
			dir := filepath.Dir(f.Name())
			foreign := filepath.Join(dir, "foreign")
			if phase == "foreign-entry" {
				if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = cleanup()
			if err == nil || !strings.Contains(err.Error(), "clean") || strings.Contains(err.Error(), "hidden") || errors.Is(err, fault) {
				t.Fatalf("unsafe cleanup failure: %v", err)
			}
			if strings.Join(removed, ",") != strings.Join([]string{f.Name(), dir}, ",") {
				t.Fatal("cleanup touched unowned paths")
			}
			if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("snapshot handle remains open")
			}
			_, fileErr := os.Lstat(f.Name())
			if (phase == "remove-file") != (fileErr == nil) {
				t.Fatal("unexpected snapshot cleanup state")
			}
			if phase == "foreign-entry" {
				got, err := os.ReadFile(foreign)
				if err != nil || string(got) != "keep" {
					t.Fatal("cleanup removed foreign entry")
				}
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "original" {
				t.Fatal("cleanup changed source")
			}
		})
	}
}

func TestObjectSnapshotFailureCleanup(t *testing.T) {
	for _, phase := range []string{"create", "copy-close", "copy-remove", "cancel-remove", "deadline-remove"} {
		t.Run(phase, func(t *testing.T) {
			path := objectTestFile(t, "source", []byte("original"))
			parent := t.TempDir()
			ops := defaultObjectFileOps()
			var dir string
			ops.mkdirTemp = func(string, string) (string, error) {
				var err error
				dir, err = os.MkdirTemp(parent, "vngcloud-object-*")
				return dir, err
			}
			fault := errors.New("hidden snapshot contents")
			var snapshot *os.File
			create := ops.create
			ops.create = func(dir, pattern string) (*os.File, error) {
				if phase == "create" {
					return nil, fault
				}
				var err error
				snapshot, err = create(dir, pattern)
				return snapshot, err
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops.copy = func(_ context.Context, dst io.Writer, _ io.Reader, _ int64) (int64, error) {
				n, err := dst.Write([]byte("partial"))
				if err != nil {
					return int64(n), err
				}
				if phase == "cancel-remove" {
					cancel()
					return int64(n), context.Canceled
				}
				if phase == "deadline-remove" {
					return int64(n), fmt.Errorf("hidden: %w", context.DeadlineExceeded)
				}
				return int64(n), fault
			}
			closeFile := ops.close
			ops.close = func(f *os.File) error {
				if phase == "copy-close" && f.Name() != path {
					return fault
				}
				return closeFile(f)
			}
			var removed []string
			remove := ops.remove
			ops.remove = func(name string) error {
				removed = append(removed, name)
				if phase != "copy-close" {
					return fault
				}
				return remove(name)
			}
			f, cleanup, err := snapshotObjectFileWithOps(ctx, path, 32, ops)
			if snapshot != nil {
				t.Cleanup(func() { _ = snapshot.Close() })
			}
			if err == nil || f != nil || cleanup != nil || strings.Contains(err.Error(), "hidden") || errors.Is(err, fault) {
				t.Fatalf("unsafe failed snapshot: %v", err)
			}
			if !strings.Contains(err.Error(), "clean") {
				t.Fatal("snapshot failure discarded cleanup failure")
			}
			if phase == "cancel-remove" && !errors.Is(err, context.Canceled) {
				t.Fatal("cleanup failure lost cancellation")
			}
			if phase == "deadline-remove" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("cleanup failure lost deadline")
			}
			wantRemoved := []string{dir}
			if snapshot != nil {
				wantRemoved = []string{snapshot.Name(), dir}
				if _, err := snapshot.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatal("failed snapshot handle remains open")
				}
			}
			if strings.Join(removed, ",") != strings.Join(wantRemoved, ",") {
				t.Fatal("failed snapshot touched unowned paths")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "original" {
				t.Fatal("failed snapshot changed source")
			}
		})
	}
}

type objectFaultWriter struct {
	dst io.Writer
	err error
}

func (w objectFaultWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p[:1])
	if err != nil {
		return n, err
	}
	return n, w.err
}

func TestObjectDownloadWriteFaults(t *testing.T) {
	for _, phase := range []string{"disk-full", "short-write"} {
		t.Run(phase, func(t *testing.T) {
			destination := objectTestFile(t, "result", []byte("old"))
			ops := defaultObjectFileOps()
			fault := errors.New("hidden disk contents")
			copyFile := ops.copy
			ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
				var writeErr error
				if phase == "disk-full" {
					writeErr = fault
				}
				return copyFile(ctx, objectFaultWriter{dst: dst, err: writeErr}, src, limit)
			}
			body := &objectTestBody{Reader: strings.NewReader("new bytes")}
			result, err := downloadObjectFileWithOps(context.Background(), body, destination, true, ops)
			if err == nil || result.Published || !body.closed || strings.Contains(err.Error(), "hidden") || errors.Is(err, fault) {
				t.Fatalf("unsafe write failure: %v", err)
			}
			got, err := os.ReadFile(destination)
			if err != nil || string(got) != "old" {
				t.Fatal("write failure changed old destination")
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 || entries[0].Name() != "result" {
				t.Fatal("write failure left temporary file")
			}
		})
	}
}

func TestObjectDownloadFinalBytesEOF(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "result")
	body := &objectTestBody{Reader: objectFaultReader{err: io.EOF}}
	result, err := downloadObjectFile(context.Background(), body, destination, false)
	if err != nil || !result.Published || result.ContentLength != 1 || !body.closed {
		t.Fatalf("final bytes with clean EOF rejected: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != "x" {
		t.Fatal("final bytes with clean EOF lost")
	}
}
