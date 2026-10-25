import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const desktop = fileURLToPath(new URL("..", import.meta.url));
const root = path.resolve(desktop, "../..");
const cache = path.join(desktop, ".dev/e2e-build");
const server = path.join(cache, "server");
const hash = createHash("sha256");
hash.update(fs.readFileSync(fileURLToPath(import.meta.url)));
const files = execFileSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard"], { cwd: root }).toString().split("\0").filter(Boolean);
files.push("go.work", "go.work.sum");
for (const file of [...new Set(files)].sort()) {
  if (file.endsWith("_test.go") || file.startsWith("cmd/desktop/testdata/") || file.startsWith("cmd/desktop/e2e/")) continue;
  if (!file.startsWith("internal/") && !file.startsWith("cmd/desktop/") && !file.endsWith(".go") && !/^(go\.(mod|sum|work|work\.sum)|pnpm-lock\.yaml|pnpm-workspace\.yaml|package\.json)$/.test(file)) continue;
  if (file.startsWith("cmd/desktop/frontend/dist/")) continue;
  const location = path.join(root, file);
  if (fs.existsSync(location) && fs.statSync(location).isFile()) hash.update(file).update("\0").update(fs.readFileSync(location)).update("\0");
}
const goEnv = execFileSync("go", ["env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOVERSION", "GOWORK", "GOEXPERIMENT", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"], { cwd: desktop });
hash.update(goEnv);
const workspace = JSON.parse(goEnv).GOWORK;
if (workspace && workspace !== "off") {
  hash.update(workspace).update(fs.readFileSync(workspace));
  if (fs.existsSync(`${workspace}.sum`)) hash.update(fs.readFileSync(`${workspace}.sum`));
}
hash.update(process.version).update(execFileSync("pnpm", ["--version"]));
const key = hash.digest("hex");
const stamp = path.join(cache, "inputs");
if (process.env.E2E_REBUILD !== "1" && fs.existsSync(server) && fs.existsSync(stamp) && fs.readFileSync(stamp, "utf8") === key && fs.existsSync(path.join(desktop, "frontend/dist/index.html"))) {
  console.error("复用 E2E 构建");
} else {
  console.error("构建前端与 server 模式二进制…");
  fs.mkdirSync(cache, { recursive: true });
  execFileSync("pnpm", ["build"], { cwd: path.join(desktop, "frontend"), stdio: ["ignore", 2, 2] });
  const temporary = `${server}.${process.pid}`;
  try {
    execFileSync("go", ["build", "-tags", "server,production", "-o", temporary, "."], { cwd: desktop, stdio: ["ignore", 2, 2] });
    fs.renameSync(temporary, server);
    fs.writeFileSync(stamp, key);
  } finally {
    fs.rmSync(temporary, { force: true });
  }
}
console.log(server);
