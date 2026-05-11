import * as fs from "node:fs";
import * as path from "node:path";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  ENV_VAR,
  SOCKET_NAME,
  resolveHome,
  socketPath,
} from "../src/home.js";
import { cleanupShortTmp, makeShortTmp } from "./stub-daemon.js";

describe("home", () => {
  let tmp: string;
  const originalEnv: Record<string, string | undefined> = {};

  beforeEach(() => {
    tmp = makeShortTmp();
    originalEnv["DAIMON_HOME"] = process.env[ENV_VAR];
    originalEnv["TMPDIR"] = process.env["TMPDIR"];
    originalEnv["XDG_CONFIG_HOME"] = process.env["XDG_CONFIG_HOME"];
  });

  afterEach(() => {
    for (const [k, v] of Object.entries(originalEnv)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
    cleanupShortTmp(tmp);
  });

  it("resolveHome honors the env var", () => {
    const target = path.join(tmp, "explicit-home");
    process.env[ENV_VAR] = target;
    const got = resolveHome();
    expect(got).toBe(fs.realpathSync(target));
    expect(fs.statSync(got).isDirectory()).toBe(true);
    if (process.platform !== "win32") {
      const mode = fs.statSync(got).mode & 0o777;
      expect(mode).toBe(0o700);
    }
  });

  it("resolveHome creates the directory when missing", () => {
    const target = path.join(tmp, "nested", "home");
    expect(fs.existsSync(target)).toBe(false);
    process.env[ENV_VAR] = target;
    const got = resolveHome();
    expect(fs.statSync(got).isDirectory()).toBe(true);
  });

  it("resolveHome rejects when the path is a file", () => {
    const target = path.join(tmp, "not-a-dir");
    fs.writeFileSync(target, "oops");
    process.env[ENV_VAR] = target;
    expect(() => resolveHome()).toThrow(/not a directory/);
  });

  it("socketPath returns primary when short", () => {
    const home = path.join(tmp, "h");
    fs.mkdirSync(home);
    const { path: p, fallbackUsed } = socketPath(home);
    expect(p).toBe(path.join(home, SOCKET_NAME));
    expect(fallbackUsed).toBe(false);
  });

  it("socketPath falls back when path too long", () => {
    let deep = tmp;
    while (Buffer.byteLength(path.join(deep, SOCKET_NAME), "utf8") <= 104) {
      deep = path.join(deep, "x".repeat(16));
      fs.mkdirSync(deep, { recursive: true });
    }
    process.env["TMPDIR"] = tmp;
    const { path: p, fallbackUsed } = socketPath(deep);
    expect(fallbackUsed).toBe(true);
    expect(path.basename(p)).toMatch(/^daimon-/);
    expect(path.extname(p)).toBe(".sock");
  });
});
