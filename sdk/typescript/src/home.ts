/**
 * DAIMON_HOME and socket-path resolution.
 *
 * Mirrors internal/daimonhome/daimonhome.go and sdk/python/daimon/_home.py
 * so the three implementations cannot disagree about where the daemon's
 * socket lives.
 *
 * Resolution order:
 *   1. $DAIMON_HOME if set
 *   2. Platform default:
 *      - macOS:   ~/Library/Application Support/daimon
 *      - Linux:   $XDG_CONFIG_HOME/daimon (default ~/.config/daimon)
 *      - Windows: %APPDATA%/daimon
 *
 * Socket path: $DAIMON_HOME/daimon.sock, with a transparent fallback to
 * $TMPDIR/daimon-<uid>.sock when the primary path exceeds the AF_UNIX
 * sun_path cap (104 bytes — conservative darwin limit).
 */

import { Buffer } from "node:buffer";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as process from "node:process";

export const ENV_VAR = "DAIMON_HOME";
export const DIR_NAME = "daimon";
export const SOCKET_NAME = "daimon.sock";

const SUN_PATH_LIMIT = 104;

export function resolveHome(): string {
  const env = process.env[ENV_VAR];
  const home = env ? path.resolve(env) : platformDefault();
  ensureDir(home);
  // Follow symlinks so macOS /tmp -> /private/tmp matches Python's
  // Path.resolve() and the Go side's filepath.EvalSymlinks usage.
  return fs.realpathSync(home);
}

export interface SocketResolution {
  path: string;
  fallbackUsed: boolean;
}

export function socketPath(home: string): SocketResolution {
  const primary = path.join(home, SOCKET_NAME);
  if (Buffer.byteLength(primary, "utf8") <= SUN_PATH_LIMIT) {
    return { path: primary, fallbackUsed: false };
  }
  const alt = tmpFallback();
  if (Buffer.byteLength(alt, "utf8") > SUN_PATH_LIMIT) {
    throw new Error(
      `socket path too long for AF_UNIX (primary=${primary.length} > ` +
        `${SUN_PATH_LIMIT}, $TMPDIR fallback also too long): ` +
        `set ${ENV_VAR} to a shorter path`,
    );
  }
  return { path: alt, fallbackUsed: true };
}

function platformDefault(): string {
  const home = os.homedir();
  const plat = process.platform;
  if (plat === "darwin") {
    return path.join(home, "Library", "Application Support", DIR_NAME);
  }
  if (plat === "win32") {
    const appdata = process.env["APPDATA"];
    if (appdata) return path.join(appdata, DIR_NAME);
    return path.join(home, "AppData", "Roaming", DIR_NAME);
  }
  const xdg = process.env["XDG_CONFIG_HOME"];
  if (xdg) return path.join(xdg, DIR_NAME);
  return path.join(home, ".config", DIR_NAME);
}

function tmpFallback(): string {
  const tmp = process.env["TMPDIR"] || "/tmp";
  fs.mkdirSync(tmp, { recursive: true });
  let tag: string;
  if (process.platform === "win32") {
    tag = process.env["USERNAME"] || "default";
  } else {
    // process.getuid is undefined on Windows; we're already in the !win branch.
    const uidFn = (process as typeof process & { getuid?: () => number }).getuid;
    tag = uidFn ? String(uidFn()) : "default";
  }
  return path.join(tmp, `daimon-${tag}.sock`);
}

function ensureDir(p: string): void {
  if (fs.existsSync(p)) {
    const stat = fs.statSync(p);
    if (!stat.isDirectory()) {
      throw new Error(`${p} exists and is not a directory`);
    }
    return;
  }
  fs.mkdirSync(p, { recursive: true, mode: 0o700 });
}
