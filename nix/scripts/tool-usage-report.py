#!/usr/bin/env python3
"""Read-only CLI-tool usage reporter for this nix-config.

Joins three signals to suggest which CLI tools to INSTALL declaratively and
which to REMOVE — never editing anything, just emitting a markdown report a
human reviews before touching the .nix files.

Signals
-------
  agents  : every Bash tool_use command across ~/.claude/projects/**/*.jsonl
  human   : every command in ~/.zsh_history (EXTENDED_HISTORY epoch format)
  on-PATH : /run/current-system/sw/bin/* resolved to its owning store package
  declared: the explicit `with pkgs; [ ... ]` lists in the repo's .nix files

Decision rules
--------------
  INSTALL candidate : a package repeatedly pulled in ad-hoc via
                      `nix-shell -p X` / `nix run nixpkgs#X` / `nix shell
                      nixpkgs#X`, that is NOT already on PATH. Each such call is
                      a literal "I needed X and it wasn't installed" event.
  REMOVE candidate  : a DECLARED package whose binary never appears in either
                      usage corpus, and that is not exempt (no-CLI outputs like
                      fonts/terminfo, or infra/recovery tools on the pin-list).

The REMOVE list is deliberately conservative: "never typed" != "removable".
Most declared packages are legitimately never typed (build toolchains,
pre-commit linters, services, shell-sourced tools, libraries, recovery tools).
Output is a SHORT human-reviewed candidate list, not an auto-delete.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import Counter
from pathlib import Path

HOME = Path.home()
REPO = Path(__file__).resolve().parents[2]  # nix-config/

PROJECTS_DIR = HOME / ".claude" / "projects"
ZSH_HISTORY = HOME / ".zsh_history"
SW_BIN = Path("/run/current-system/sw/bin")

# .nix files whose `with pkgs; [ ... ]` lists hold explicitly-declared packages.
DECL_FILES = [
    REPO / "nix/modules/nixos/profiles/core.nix",
    REPO / "nix/modules/nixos/profiles/dev.nix",
    REPO / "nix/modules/nixos/profiles/desktop.nix",
    REPO / "nix/hosts/fw13/default.nix",
    REPO / "nix/hosts/t480/default.nix",
]

# Shell builtins / keywords / control words that are never packages. Leading
# token after splitting that lands here is dropped.
BUILTINS = {
    "cd", "echo", "export", "set", "unset", "alias", "unalias", "source", ".",
    "eval", "exec", "exit", "return", "shift", "read", "let", "local", "declare",
    "typeset", "pushd", "popd", "dirs", "true", "false", "test", "[", "[[", "]]",
    "then", "else", "elif", "fi", "do", "done", "for", "while", "until", "if",
    "case", "esac", "in", "function", "select", "time", "trap", "wait", "jobs",
    "bg", "fg", "kill", "disown", "type", "hash", "help", "builtin", "command",
    "printf", "pwd", "umask", "ulimit", "getopts", "shopt", "setopt", "unsetopt",
    "history", "fc", "bindkey", "zstyle", "autoload", "compinit", "emulate",
    "z", "zi",  # zoxide shell functions, not the `zoxide` binary
    "EOF", "EOL", "END", "PASS", "FAIL", "OK", "yes", "no",
}

# Language keywords / identifiers that leak in from `python3 -c '...'`, `node
# -e`, heredoc bodies and inline code blocks. The splitter tokenizes those
# payloads, so without this they rank as if they were commands.
CODE_NOISE = {
    "print", "const", "let", "var", "import", "from", "as", "def", "class",
    "return", "await", "async", "require", "module", "exports", "console.log",
    "try", "catch", "finally", "raise", "yield", "lambda", "self", "pass",
    "with", "elif", "True", "False", "None", "null", "undefined", "new",
    "public", "private", "static", "void", "int", "str", "bool", "float",
    "println", "fmt", "printf", "f", "p", "puts", "throw", "switch", "default",
}
BUILTINS |= CODE_NOISE

# Wrapper commands: skip them and look at the *next* token as the real command.
WRAPPERS = {"sudo", "doas", "env", "nice", "time", "timeout", "command",
            "builtin", "exec", "xargs", "nohup", "stdbuf", "setsid", "watch"}

# Declared attr -> the binary you'd actually type. Only the renames; attrs whose
# binary == attr need no entry.
ATTR_TO_BIN = {
    "ripgrep": "rg",
    "fd": "fd",
    "claude-code": "claude",
    "gemini-cli-bin": "gemini",
    "lm_sensors": "sensors",
    "pciutils": "lspci",
    "usbutils": "lsusb",
    "btrfs-progs": "btrfs",
    "android-tools": "adb",
    "clang-tools": "clangd",
    "nodejs_latest": "node",
    "jdk21": "java",
    "wl-clipboard": "wl-copy",
    "nixpkgs-fmt": "nixpkgs-fmt",
    "tree-sitter": "tree-sitter",
}

# Declared attrs that ship NO typed CLI binary (fonts, terminfo, libraries,
# GUI-only apps launched from a menu). Never REMOVE candidates via usage.
NO_CLI = {
    "kitty.terminfo", "setupDotfiles", "stow",
}

# Declared but legitimately rarely/never typed yet load-bearing. Excluded from
# REMOVE flagging so the report stays a short, real candidate list. Edit freely.
INFRA_PINS = {
    # build / eval-time toolchains
    "gcc", "gnumake", "clang", "clang-tools", "jdk21", "gradle", "tree-sitter",
    "arduino-cli", "android-tools",
    # pre-commit / formatter tooling (invoked by the hook framework, not typed)
    "pre-commit", "shellcheck", "shfmt", "stylua", "gitleaks", "nixpkgs-fmt",
    # secrets / recovery / hardware (used rarely but must stay)
    "age", "sops", "ssh-to-age", "btrfs-progs", "lm_sensors", "pciutils",
    "usbutils", "acpi", "nvme-cli", "traceroute", "unzip",
    # service-backed (run as a daemon, not typed)
    "tailscale", "syncthing",
    # shell-sourced (sourced into zsh, never invoked by name)
    "starship", "zoxide", "fzf",
}

# Splits a command line into candidate sub-commands.
SPLIT_RE = re.compile(r"[\n;|&]+|\$\(|`|\)|\(|\bxargs\b")
ENV_ASSIGN_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")
TOKEN_RE = re.compile(r"^[A-Za-z][A-Za-z0-9._+-]*$")

# Inline-code payloads (`python3 -c '...'`, `node -e "..."`, perl/ruby/awk -e)
# and heredoc bodies. Their contents are code, not commands — drop before
# splitting so language keywords don't pollute the tally.
INLINE_CODE_RE = re.compile(
    r"""\b(?:python3?|node|perl|ruby|bash|sh|zsh|awk|jq)\b[^\n'"]*?"""
    r"""\s-(?:c|e)\s+('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")""",
    re.DOTALL,
)
HEREDOC_RE = re.compile(r"<<-?\s*'?(\w+)'?.*?^\s*\1\b", re.DOTALL | re.MULTILINE)


def strip_inline_code(cmd: str) -> str:
    cmd = HEREDOC_RE.sub(" ", cmd)
    cmd = INLINE_CODE_RE.sub(lambda m: m.group(0)[:m.start(1) - m.start(0)], cmd)
    return cmd


def first_command_token(segment: str) -> str | None:
    """Return the real command name from one shell segment, or None."""
    words = segment.strip().split()
    i = 0
    while i < len(words):
        w = words[i]
        if ENV_ASSIGN_RE.match(w):  # FOO=bar prefix
            i += 1
            continue
        if w in WRAPPERS:
            # `timeout 5 cmd` -> skip the numeric arg too
            i += 1
            while i < len(words) and (words[i].lstrip("-").replace(".", "").isdigit()
                                       or words[i].startswith("-")):
                i += 1
            continue
        tok = os.path.basename(w)
        if not TOKEN_RE.match(tok):
            return None
        if tok in BUILTINS:
            return None
        return tok
    return None


# Patterns that pull a package in ad-hoc (the INSTALL signal).
NIXSHELL_P_RE = re.compile(r"\bnix-shell\b([^\n;|&]*)")
NIXREF_RE = re.compile(r"\bnix\s+(?:run|shell)\b([^\n;|&]*)")


def extract_ephemeral(cmd: str, tally: Counter) -> None:
    """Record packages pulled via nix-shell -p / nix run|shell nixpkgs#X."""
    for m in NIXSHELL_P_RE.finditer(cmd):
        rest = m.group(1)
        pm = re.search(r"-p\s+([^\n;|&]*?)(?:--run\b|-c\b|$)", rest)
        if pm:
            for pkg in pm.group(1).split():
                pkg = pkg.strip()
                if TOKEN_RE.match(pkg.split("#")[-1]):
                    tally[pkg.split("#")[-1]] += 1
    for m in NIXREF_RE.finditer(cmd):
        for ref in re.findall(r"nixpkgs#([A-Za-z][A-Za-z0-9._+-]*)", m.group(1)):
            tally[ref] += 1


def tokenize(cmd: str) -> list[str]:
    out = []
    cmd = strip_inline_code(cmd)
    for seg in SPLIT_RE.split(cmd):
        tok = first_command_token(seg)
        if tok:
            out.append(tok)
    return out


def scan_transcripts() -> tuple[Counter, Counter, int]:
    """Returns (command tally, ephemeral-pkg tally, transcript file count)."""
    cmds: Counter = Counter()
    eph: Counter = Counter()
    n = 0
    if not PROJECTS_DIR.exists():
        return cmds, eph, n
    for jf in PROJECTS_DIR.rglob("*.jsonl"):
        n += 1
        try:
            with jf.open(errors="replace") as fh:
                for line in fh:
                    if '"tool_use"' not in line or '"Bash"' not in line:
                        continue
                    try:
                        obj = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    content = (obj.get("message") or {}).get("content")
                    if not isinstance(content, list):
                        continue
                    for item in content:
                        if (isinstance(item, dict) and item.get("type") == "tool_use"
                                and item.get("name") == "Bash"):
                            cmd = (item.get("input") or {}).get("command", "")
                            if not cmd:
                                continue
                            cmds.update(tokenize(cmd))
                            extract_ephemeral(cmd, eph)
        except OSError:
            continue
    return cmds, eph, n


def scan_history() -> tuple[Counter, Counter, int]:
    """Returns (command tally, ephemeral-pkg tally, line count)."""
    cmds: Counter = Counter()
    eph: Counter = Counter()
    n = 0
    if not ZSH_HISTORY.exists():
        return cmds, eph, n
    with ZSH_HISTORY.open(errors="replace") as fh:
        for line in fh:
            n += 1
            # EXTENDED_HISTORY: ": <epoch>:<elapsed>;<cmd>"
            cmd = line.split(";", 1)[1] if line.startswith(":") and ";" in line else line
            cmds.update(tokenize(cmd))
            extract_ephemeral(cmd, eph)
    return cmds, eph, n


def on_path_packages() -> tuple[set[str], dict[str, str], dict[str, Path]]:
    """Binaries on PATH, a binary -> owning store pkg-name map, and a
    binary -> owning store ROOT path map (for .desktop detection)."""
    bins: set[str] = set()
    owner: dict[str, str] = {}
    roots: dict[str, Path] = {}
    if not SW_BIN.exists():
        return bins, owner, roots
    pkg_re = re.compile(r"(/nix/store/[a-z0-9]{32}-(.+?))/")
    for entry in SW_BIN.iterdir():
        name = entry.name
        bins.add(name)
        try:
            target = os.path.realpath(entry)
        except OSError:
            continue
        m = pkg_re.search(target)
        if m:
            owner[name] = m.group(2)
            roots[name] = Path(m.group(1))
    return bins, owner, roots


def parse_declared() -> dict[str, list[Path]]:
    """attr-name -> files that declare it, from `with pkgs; [ ... ]` blocks."""
    declared: dict[str, list[Path]] = {}
    block_re = re.compile(r"with pkgs;\s*\[(.*?)\]", re.DOTALL)
    ident_re = re.compile(r"^[A-Za-z][A-Za-z0-9._-]*$")
    for f in DECL_FILES:
        if not f.exists():
            continue
        text = f.read_text(errors="replace")
        for block in block_re.findall(text):
            for raw in block.splitlines():
                line = raw.split("#", 1)[0].strip()  # drop inline comments
                if not line:
                    continue
                for tok in line.split():
                    # normalize pkgs./unstable. prefixes to the bare attr
                    attr = tok
                    for pre in ("pkgs.unstable.", "pkgs.", "unstable."):
                        if attr.startswith(pre):
                            attr = attr[len(pre):]
                    if ident_re.match(attr):
                        declared.setdefault(attr, [])
                        if f not in declared[attr]:
                            declared[attr].append(f)
    return declared


def attr_provides(attr: str, path_bins: set[str],
                  owner: dict[str, str]) -> set[str]:
    """Binaries ON PATH that this declared attr provides. Uses the store's
    binary->package ownership (so `coreutils` -> {ls,cat,...}, `ripgrep` ->
    {rg}) plus the rename map and the attr-as-binary fallback."""
    provided = {b for b, pkg in owner.items()
                if pkg == attr or pkg.startswith(attr + "-")}
    mapped = ATTR_TO_BIN.get(attr)
    if mapped and mapped in path_bins:
        provided.add(mapped)
    if attr in path_bins:
        provided.add(attr)
    return provided


def is_gui_app(provides: set[str], roots: dict[str, Path]) -> bool:
    """True if any provided binary's package ships a desktop entry — i.e. it's
    launched from a menu/rofi, so the shell never sees it (usage unobservable)."""
    for b in provides:
        root = roots.get(b)
        if root and (root / "share" / "applications").is_dir():
            try:
                if any(root.joinpath("share", "applications").glob("*.desktop")):
                    return True
            except OSError:
                pass
    return False


def usage(provides: set[str], agents: Counter, human: Counter) -> int:
    return sum(agents.get(b, 0) + human.get(b, 0) for b in provides)


def rel(p: Path) -> str:
    try:
        return str(p.relative_to(REPO))
    except ValueError:
        return str(p)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--top", type=int, default=25, help="rows in TOP-USED tables")
    ap.add_argument("--min-ephemeral", type=int, default=2,
                    help="min ad-hoc pulls to flag an INSTALL candidate")
    args = ap.parse_args()

    a_cmds, a_eph, n_files = scan_transcripts()
    h_cmds, h_eph, n_hist = scan_history()
    path_bins, owner, roots = on_path_packages()
    declared = parse_declared()

    eph = Counter()
    eph.update(a_eph)
    eph.update(h_eph)

    out = []
    w = out.append
    w("# CLI tool usage report\n")
    w("_Read-only. Candidate lists for human review — nothing is edited._\n")
    w(f"- agent transcripts scanned: **{n_files}** "
      f"(`~/.claude/projects/**/*.jsonl`)")
    w(f"- zsh history lines scanned: **{n_hist}** (`~/.zsh_history`)")
    w(f"- binaries on PATH: **{len(path_bins)}** (`/run/current-system/sw/bin`)")
    w(f"- explicitly-declared attrs: **{len(declared)}** "
      f"(across {len(DECL_FILES)} .nix files)\n")

    # --- TOP USED -------------------------------------------------------
    w("## Most-used by agents\n")
    w("| tool | count | on PATH? |")
    w("|------|------:|:--------:|")
    for tok, c in a_cmds.most_common(args.top):
        w(f"| `{tok}` | {c} | {'✓' if tok in path_bins else '—'} |")
    w("")
    w("## Most-used by human (zsh history)\n")
    w("| tool | count | on PATH? |")
    w("|------|------:|:--------:|")
    for tok, c in h_cmds.most_common(args.top):
        w(f"| `{tok}` | {c} | {'✓' if tok in path_bins else '—'} |")
    w("")

    # --- INSTALL candidates --------------------------------------------
    w("## INSTALL candidates\n")
    w("Packages pulled in ad-hoc via `nix-shell -p` / `nix run|shell "
      "nixpkgs#…`, not already on PATH. Each pull = a 'needed but not "
      "installed' event.\n")
    install = [(p, c) for p, c in eph.most_common()
               if c >= args.min_ephemeral
               and p not in path_bins
               and p not in declared]
    if install:
        w("| package | ad-hoc pulls |")
        w("|---------|-------------:|")
        for p, c in install:
            w(f"| `{p}` | {c} |")
    else:
        w(f"_None at threshold ≥{args.min_ephemeral}. "
          "Lower with `--min-ephemeral 1` to see one-offs._")
    w("")
    # also surface ad-hoc pulls of things already on PATH (declaration drift)
    already = [(p, c) for p, c in eph.most_common()
               if c >= args.min_ephemeral and (p in path_bins or p in declared)]
    if already:
        w("<details><summary>Ad-hoc pulls of tools already available "
          f"({len(already)}) — usually harmless habit, not action items"
          "</summary>\n")
        for p, c in already:
            w(f"- `{p}` ×{c}")
        w("\n</details>\n")

    # --- categorize every declared attr --------------------------------
    removes = []        # CLI tool, provides a binary, never used
    gui_unused = []     # GUI app (.desktop), unused in shell (unobservable)
    exempt_nocli = []   # provides no binary on PATH (font / lib / theme)
    exempt_pin = []     # on the infra/recovery/service pin-list, unused
    for attr in sorted(declared):
        provides = attr_provides(attr, path_bins, owner)
        u = usage(provides, a_cmds, h_cmds)
        if u > 0:
            continue  # used — keep, nothing to report
        if attr in NO_CLI or not provides:
            exempt_nocli.append(attr)
        elif attr in INFRA_PINS:
            exempt_pin.append((attr, provides))
        elif is_gui_app(provides, roots):
            gui_unused.append((attr, provides, declared[attr]))
        else:
            removes.append((attr, provides, declared[attr]))

    w("## REMOVE candidates\n")
    w("Declared CLI tools whose binary never appears in either usage corpus. "
      "No-CLI outputs (fonts/libs/themes), GUI apps, and infra/recovery/"
      "service pins are split out below. Verify each is a removable top-level "
      "want (`nix why-depends /run/current-system/sw <attr>`) before deleting.\n")
    if removes:
        w("| declared attr | binaries | declared in |")
        w("|---------------|----------|-------------|")
        for attr, provides, files in removes:
            bs = ", ".join(f"`{b}`" for b in sorted(provides)[:4])
            w(f"| `{attr}` | {bs} | {', '.join(rel(f) for f in files)} |")
    else:
        w("_None — every declared, typeable CLI tool shows usage._")
    w("")

    if gui_unused:
        w("### GUI apps with no shell usage\n")
        w("Launched from a menu/rofi, so the shell never sees them — usage is "
          "**not observable here**. Review by memory, not by this data.\n")
        for attr, _provides, files in gui_unused:
            w(f"- `{attr}` ({', '.join(rel(f) for f in files)})")
        w("")

    if exempt_pin:
        w("<details><summary>Declared + unused but PINNED as infra/recovery/"
          f"service ({len(exempt_pin)})</summary>\n")
        for attr, _p in exempt_pin:
            w(f"- `{attr}`")
        w("\n</details>\n")
    if exempt_nocli:
        w("<details><summary>Declared with no CLI binary — fonts / libs / "
          f"themes / drivers ({len(exempt_nocli)})</summary>\n")
        w(", ".join(f"`{a}`" for a in exempt_nocli))
        w("\n</details>\n")

    w("---")
    w("_Caveats: zsh history is thin (`HISTDUP=erase`) so human counts are "
      "dampened — trust presence/absence over magnitude. GUI apps launched "
      "from rofi never touch the shell and won't appear. Heredoc/code-block "
      "noise is filtered but imperfect. Edit `INFRA_PINS` / `ATTR_TO_BIN` / "
      "`NO_CLI` at the top of this script to tune._")

    print("\n".join(out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
