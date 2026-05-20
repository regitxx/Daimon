package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/regitxx/Daimon/internal/daimonhome"
	"github.com/regitxx/Daimon/internal/secretbox"
)

// cmdBackup + cmdRestore implement the whole-daimon migration path:
//
//   daimon backup --to my-daimon.dbk         # default: encrypted
//   daimon backup --to plain.tgz --no-encrypt
//   daimon restore my-daimon.dbk             # auto-detects encryption
//
// The use cases are: migrate a daimon to a new machine, take periodic
// snapshots, archive a daimon for cold storage, hand a daimon to a
// trusted operator. Both commands are offline-only — they refuse to
// run while daimond is listening on the socket — for the same reason
// recover + rotate-password are: a hot snapshot mid-activity-log-write
// could capture a torn line, and a hot restore would replace files
// under a running process's open file handles.
//
// Backup file format (encrypted, the default):
//
//   "DAIMONBACKUPv1\n"          (15 bytes, magic + format version)
//   "encrypted\n"                (10 bytes, mode marker — vs "plain\n")
//   salt:    16 bytes raw         (Argon2id salt)
//   nonce:   12 bytes raw         (AES-GCM nonce)
//   ciphertext: ...bytes          (AES-256-GCM seal of gzipped tarball)
//
// Plain mode:
//
//   "DAIMONBACKUPv1\n"          (15 bytes, magic + format version)
//   "plain\n"                    (6 bytes, mode marker)
//   gzipped tarball              (the rest of the file)
//
// Both modes carry the same magic header so `daimon restore` can
// detect mode from the first 21–25 bytes. Files inside the tarball
// have RELATIVE paths (e.g. "identity.keystore"), not absolute paths
// rooted at the source $DAIMON_HOME — so a backup from
// /Users/alice/daimon can restore into /Users/bob/daimon-restored
// without leaking either path.

const (
	backupMagicLine   = "DAIMONBACKUPv1\n"
	backupModeEnc     = "encrypted\n"
	backupModePlain   = "plain\n"
	backupSaltLen     = 16
	backupArgonTime   = 3
	backupArgonMemKiB = 64 * 1024
	backupArgonParam  = 4
	backupKeyLen      = 32
	backupAAD         = "daimon-backup-v1"
)

// Files in $DAIMON_HOME we back up. Anything else (sockets, logs,
// .rotate-tmp leftovers, etc.) is deliberately omitted — `daimon.sock`
// is a runtime artifact, `daimon.log` is debugging noise, temp files
// are mid-rotation state.
var backupFiles = []string{
	"identity.keystore",
	"wallet.keystore",
	"memory.db",
	"activity.log",
}

// --- daimon backup ----------------------------------------------------------

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("daimon backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	toPath := fs.String("to", "", "path to write the backup file (required)")
	noEncrypt := fs.Bool("no-encrypt", false, "skip the Argon2id+AES-GCM outer envelope (the inner keystores remain encrypted regardless)")
	force := fs.Bool("force", false, "overwrite an existing file at --to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *toPath == "" {
		return fmt.Errorf("--to is required (e.g. --to my-daimon-backup.dbk)")
	}
	if _, err := os.Stat(*toPath); err == nil && !*force {
		return fmt.Errorf("%s already exists — use --force to overwrite", *toPath)
	}

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}

	// Refuse to back up while the daemon is listening — same posture
	// as rotate-password. A torn activity.log mid-append could land in
	// the tarball and the restored daimon would fail `activity verify`.
	if socket, _, err := daimonhome.SocketPath(home); err == nil {
		if conn, derr := net.DialTimeout("unix", socket, 200*time.Millisecond); derr == nil {
			_ = conn.Close()
			return fmt.Errorf("daimond is currently listening on %s — stop it before backing up "+
				"(`pkill daimond`, then re-run this command). Running daemons can have a torn "+
				"activity.log mid-append, which would land an unverifiable chain in the backup",
				socket)
		}
	}

	// Build the gzipped tarball in memory. Almost all users have <100MB
	// of daimon state; streaming the tar through gzip and AES-GCM
	// without buffering would save memory but add complexity (AES-GCM
	// needs the full ciphertext at verify time anyway). Keep it simple.
	tarballBytes, included, err := buildTarball(home)
	if err != nil {
		return fmt.Errorf("build tarball: %w", err)
	}
	if len(included) == 0 {
		return fmt.Errorf("no daimon files found at %s — has `daimon init` been run?", home)
	}

	var fileBytes []byte
	if *noEncrypt {
		fileBytes = encodePlainBackup(tarballBytes)
	} else {
		// Prompt for passphrase + confirm. The passphrase protects the
		// backup file at rest IN ADDITION TO the inner keystore passwords
		// already protecting identity.keystore + wallet.keystore.
		fmt.Fprintln(os.Stderr, "Choose a backup passphrase. This is SEPARATE from your daimon")
		fmt.Fprintln(os.Stderr, "unlock password — the backup file gets encrypted under this")
		fmt.Fprintln(os.Stderr, "passphrase, and the keystores inside it stay encrypted under")
		fmt.Fprintln(os.Stderr, "your existing daimon password.")
		passphrase, err := readPassword("Backup passphrase:  ")
		if err != nil {
			return err
		}
		defer zero(passphrase)
		if len(passphrase) == 0 {
			return errors.New("backup passphrase must not be empty (use --no-encrypt for a plain backup)")
		}
		confirm, err := readPassword("Confirm passphrase: ")
		if err != nil {
			return err
		}
		defer zero(confirm)
		if string(passphrase) != string(confirm) {
			return errors.New("passphrases did not match")
		}

		fileBytes, err = encodeEncryptedBackup(tarballBytes, passphrase)
		if err != nil {
			return fmt.Errorf("encrypt backup: %w", err)
		}
	}

	if err := os.WriteFile(*toPath, fileBytes, 0600); err != nil {
		return fmt.Errorf("write %s: %w", *toPath, err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Backup written.")
	fmt.Fprintf(os.Stderr, "  Path:  %s (mode 0600)\n", *toPath)
	fmt.Fprintf(os.Stderr, "  Size:  %s\n", humanBytes(int64(len(fileBytes))))
	fmt.Fprintf(os.Stderr, "  Mode:  ")
	if *noEncrypt {
		fmt.Fprintln(os.Stderr, "plain (inner keystores still encrypted under your daimon password)")
	} else {
		fmt.Fprintln(os.Stderr, "encrypted (Argon2id + AES-256-GCM outer envelope)")
	}
	fmt.Fprintln(os.Stderr, "  Includes:")
	for _, name := range included {
		fmt.Fprintf(os.Stderr, "    %s\n", name)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "Restore: `daimon restore %s` against a fresh $DAIMON_HOME.\n", *toPath)
	return nil
}

// buildTarball gzips a tar of $DAIMON_HOME's backupFiles into memory.
// Returns the bytes + the list of included file names (so the caller
// can print a summary). Files that don't exist are silently skipped —
// e.g. a fresh post-init daimon won't have wallet.keystore or memory.db
// yet, and that's a valid state to back up.
func buildTarball(home string) ([]byte, []string, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	var included []string
	for _, name := range backupFiles {
		path := filepath.Join(home, name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("%s is not a regular file (mode=%s)", path, info.Mode())
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		hdr := &tar.Header{
			Name:    name, // relative — restore works against any $DAIMON_HOME
			Mode:    int64(info.Mode().Perm()),
			Size:    int64(len(body)),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, nil, fmt.Errorf("tar header %s: %w", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return nil, nil, fmt.Errorf("tar body %s: %w", name, err)
		}
		included = append(included, name)
	}
	if err := tw.Close(); err != nil {
		return nil, nil, fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, nil, fmt.Errorf("gzip close: %w", err)
	}
	return buf.Bytes(), included, nil
}

func encodePlainBackup(tarball []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(backupMagicLine)
	buf.WriteString(backupModePlain)
	buf.Write(tarball)
	return buf.Bytes()
}

func encodeEncryptedBackup(tarball, passphrase []byte) ([]byte, error) {
	salt := make([]byte, backupSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("salt: %w", err)
	}
	key := argon2.IDKey(passphrase, salt, backupArgonTime, backupArgonMemKiB, backupArgonParam, backupKeyLen)

	gcm, err := secretbox.NewAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	nonce := make([]byte, secretbox.NonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, tarball, []byte(backupAAD))

	var buf bytes.Buffer
	buf.WriteString(backupMagicLine)
	buf.WriteString(backupModeEnc)
	buf.Write(salt)
	buf.Write(nonce)
	buf.Write(ciphertext)
	return buf.Bytes(), nil
}

// --- daimon restore ---------------------------------------------------------

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("daimon restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "overwrite an existing daimon at $DAIMON_HOME (DESTROYS the current daimon there)")
	dryRun := fs.Bool("dry-run", false, "verify the backup file is valid without writing anything to $DAIMON_HOME (decrypts if encrypted, prompts for passphrase, walks the tarball, reports the manifest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: daimon restore [--force] [--dry-run] <path-to-backup>")
	}
	fromPath := fs.Arg(0)

	home, err := daimonhome.Resolve()
	if err != nil {
		return err
	}

	// Same offline check as backup — except --dry-run skips it. Dry-run
	// only reads the backup file and inspects its contents in memory;
	// it never touches $DAIMON_HOME or the daemon, so there's no
	// torn-write risk that justifies refusing while the daemon's up.
	if !*dryRun {
		if socket, _, err := daimonhome.SocketPath(home); err == nil {
			if conn, derr := net.DialTimeout("unix", socket, 200*time.Millisecond); derr == nil {
				_ = conn.Close()
				return fmt.Errorf("daimond is currently listening on %s — stop it before restoring", socket)
			}
		}

		// Refuse to restore into a non-empty $DAIMON_HOME unless --force.
		// Specifically check for the backup files themselves; other files
		// like daimon.log or daimon.sock are fine to coexist (well, sock
		// shouldn't be there per the above check, but stale log is fine).
		for _, name := range backupFiles {
			path := filepath.Join(home, name)
			if _, err := os.Stat(path); err == nil {
				if !*force {
					return fmt.Errorf("%s already exists at the target — use --force to overwrite "+
						"(DESTROYS the current daimon at %s)", name, home)
				}
			}
		}

		if err := os.MkdirAll(home, 0700); err != nil {
			return fmt.Errorf("create %s: %w", home, err)
		}
	}

	fileBytes, err := os.ReadFile(fromPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", fromPath, err)
	}

	tarball, mode, err := decodeBackup(fileBytes)
	if err != nil {
		return fmt.Errorf("decode backup: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Backup mode:    %s\n", mode)
	fmt.Fprintf(os.Stderr, "Backup file:    %s (%s)\n", fromPath, humanBytes(int64(len(fileBytes))))
	if *dryRun {
		fmt.Fprintln(os.Stderr, "Dry-run:        no files will be written to $DAIMON_HOME")
	}
	if mode == "encrypted" {
		passphrase, err := readPassword("Backup passphrase: ")
		if err != nil {
			return err
		}
		defer zero(passphrase)
		if len(passphrase) == 0 {
			return errors.New("passphrase must not be empty")
		}
		// Re-call decodeBackup with the passphrase to decrypt.
		tarball, err = decryptBackup(fileBytes, passphrase)
		if err != nil {
			return fmt.Errorf("decrypt backup: %w", err)
		}
	}

	if *dryRun {
		// Walk the tarball in memory without writing. Reports each
		// entry's name + size and asserts the structure invariants
		// extractTarball would assert at write time (path-traversal
		// rejection, allowlist enforcement, gzip integrity). Catches
		// a corrupted backup BEFORE the user commits to an actual
		// restore.
		entries, err := walkTarball(tarball)
		if err != nil {
			return fmt.Errorf("verify tarball: %w", err)
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Backup verified — file is structurally valid and decryptable.")
		fmt.Fprintln(os.Stderr, "  Contents:")
		for _, e := range entries {
			fmt.Fprintf(os.Stderr, "    %-20s %s\n", e.name, humanBytes(e.size))
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "To actually restore, re-run without --dry-run:\n")
		fmt.Fprintf(os.Stderr, "  daimon restore %s\n", fromPath)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Note: dry-run verifies the BACKUP FILE'S integrity (gzip, tar, encryption envelope).")
		fmt.Fprintln(os.Stderr, "It does NOT verify the inner activity chain — that requires the daimon password and")
		fmt.Fprintln(os.Stderr, "happens automatically on `daimon activity verify` after a real restore + unlock.")
		return nil
	}

	included, err := extractTarball(tarball, home)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Restore complete.")
	fmt.Fprintf(os.Stderr, "  Source:    %s\n", fromPath)
	fmt.Fprintf(os.Stderr, "  Target:    %s\n", home)
	fmt.Fprintln(os.Stderr, "  Restored:")
	for _, name := range included {
		fmt.Fprintf(os.Stderr, "    %s\n", name)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next: `daimon unlock` to bring up the restored daimon against this $DAIMON_HOME.")
	fmt.Fprintln(os.Stderr, "(You'll need the same password the daimon was originally encrypted under —")
	fmt.Fprintln(os.Stderr, "the BACKUP passphrase only protected the file in transit, the inner keystore")
	fmt.Fprintln(os.Stderr, "passwords are unchanged.)")
	return nil
}

// tarEntry captures just the name + size of a tarball entry — what
// dry-run mode reports to the user. Doesn't include the body because
// dry-run is in-memory-only and we don't need to retain bytes once
// we've verified the structure.
type tarEntry struct {
	name string
	size int64
}

// walkTarball reads the same gzipped-tar stream extractTarball does
// but DOES NOT write files. Used by --dry-run mode to verify a backup
// without modifying disk. Applies the same structural checks
// extractTarball does (path-traversal rejection, allowlist
// enforcement) so a tarball that would fail real extract also fails
// dry-run.
func walkTarball(tarball []byte) ([]tarEntry, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var out []tarEntry
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar next: %w", err)
		}
		if filepath.IsAbs(hdr.Name) || hdr.Name != filepath.Base(hdr.Name) {
			return nil, fmt.Errorf("backup contains illegal path %q (must be a bare filename)", hdr.Name)
		}
		allowed := false
		for _, name := range backupFiles {
			if hdr.Name == name {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("backup contains unexpected file %q (allowed: %v)", hdr.Name, backupFiles)
		}
		// Read + discard the body to force gzip to validate the
		// underlying compressed stream. A corrupt gzip would error
		// here, on a body read, rather than on tr.Next.
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("tar read %s: %w", hdr.Name, err)
		}
		out = append(out, tarEntry{name: hdr.Name, size: int64(len(body))})
	}
	return out, nil
}

func decodeBackup(fileBytes []byte) (tarball []byte, mode string, err error) {
	if !bytes.HasPrefix(fileBytes, []byte(backupMagicLine)) {
		return nil, "", fmt.Errorf("not a daimon backup file (missing %q magic header)", backupMagicLine[:len(backupMagicLine)-1])
	}
	rest := fileBytes[len(backupMagicLine):]
	switch {
	case bytes.HasPrefix(rest, []byte(backupModePlain)):
		return rest[len(backupModePlain):], "plain", nil
	case bytes.HasPrefix(rest, []byte(backupModeEnc)):
		return nil, "encrypted", nil // caller decrypts after prompting
	default:
		return nil, "", fmt.Errorf("unknown backup mode (expected %q or %q)", backupModePlain[:len(backupModePlain)-1], backupModeEnc[:len(backupModeEnc)-1])
	}
}

func decryptBackup(fileBytes, passphrase []byte) ([]byte, error) {
	rest := fileBytes[len(backupMagicLine)+len(backupModeEnc):]
	if len(rest) < backupSaltLen+secretbox.NonceLen {
		return nil, errors.New("backup file is truncated (header present, payload missing)")
	}
	salt := rest[:backupSaltLen]
	nonce := rest[backupSaltLen : backupSaltLen+secretbox.NonceLen]
	ciphertext := rest[backupSaltLen+secretbox.NonceLen:]

	key := argon2.IDKey(passphrase, salt, backupArgonTime, backupArgonMemKiB, backupArgonParam, backupKeyLen)
	gcm, err := secretbox.NewAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("aead: %w", err)
	}
	tarball, err := gcm.Open(nil, nonce, ciphertext, []byte(backupAAD))
	if err != nil {
		return nil, errors.New("wrong passphrase or corrupted backup")
	}
	return tarball, nil
}

func extractTarball(tarball []byte, home string) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var included []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar next: %w", err)
		}
		// Defense in depth: refuse path-traversal entries. The tarball
		// SHOULD only contain bare basenames per buildTarball, but we
		// double-check before writing anywhere.
		if filepath.IsAbs(hdr.Name) || hdr.Name != filepath.Base(hdr.Name) {
			return nil, fmt.Errorf("backup contains illegal path %q (must be a bare filename)", hdr.Name)
		}
		// Also refuse anything that isn't in our backup file allowlist.
		allowed := false
		for _, name := range backupFiles {
			if hdr.Name == name {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("backup contains unexpected file %q (allowed: %v)", hdr.Name, backupFiles)
		}
		out := filepath.Join(home, hdr.Name)
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("tar read %s: %w", hdr.Name, err)
		}
		// Preserve mode from the tar entry, clamped to 0600 max — backups
		// shouldn't accidentally grant broader permissions than the
		// originals.
		mode := os.FileMode(hdr.Mode) & 0700
		if err := os.WriteFile(out, body, mode); err != nil {
			return nil, fmt.Errorf("write %s: %w", out, err)
		}
		included = append(included, hdr.Name)
	}
	return included, nil
}
