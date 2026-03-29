# vinth - velo's modrinth mod manager

<div align="center" markdown="1">
  
  ![GitHub last commit](https://img.shields.io/github/last-commit/velolib/vinth)
  ![GitHub top language](https://img.shields.io/github/languages/top/velolib/vinth)
  ![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/velolib/vinth/ci.yml?branch=main)
  ![GitHub License](https://img.shields.io/github/license/velolib/vinth)
  
</div>

## Intro

vinth is a Minecraft mod manager written in Go that uses a lockfile.

It tracks mods from Modrinth in `vinth.lock.json` so your mods folder can be recreated consistently across machines.

## Why a lockfile?

Without a lockfile, mod folders drift over time: versions change, files get renamed, dependencies are missed, and two people with "the same mods" end up with different results.

`vinth.lock.json` is the source of truth for your mods. It pins exactly what should exist (version IDs, files, URLs, and hashes), so `vinth sync` can rebuild the same state reliably.

This is the core reason vinth exists: reproducibility over guesswork.

## Pitch

### What vinth does

- Creates and manages a lockfile with your Minecraft target version and loader.
- Adds, removes, upgrades, and lists Modrinth mods in that lockfile.
- Syncs your current directory to match the lockfile (download and integrity checks).
- Copies non-recursive `.jar` files from `local/` into the current directory on every `vinth sync`.
- Checks required dependencies and can auto-add missing ones.
- Helps keep mod setups reproducible for teams/friends and future-you.

### What vinth does not do

- It is not a launcher.
- It is not a GUI app.
- It is not a CurseForge mod manager.
- It does not install Java, Minecraft, Fabric/Forge/NeoForge/Quilt runtimes, or game instances.
- It does not replace manual mod compatibility judgement for complex packs.

## Installation

### Option 1: Download prebuilt binaries (recommended)

1. Open GitHub Releases for this repo.
2. Download the archive for your OS and CPU.
3. Extract it and place `vinth` (`vinth.exe` on Windows) somewhere on your [PATH](https://www.howtogeek.com/118594/how-to-edit-your-system-path-for-easy-command-line-access/).

Windows PowerShell example after download:

```powershell
# Example: move vinth.exe into a user bin directory
New-Item -ItemType Directory -Force "$HOME\\bin" | Out-Null
Move-Item .\vinth.exe "$HOME\\bin\\vinth.exe" -Force
```

### Option 2: Build from source

Requirements:

- Go toolchain (as specified in `go.mod`)

```bash
git clone https://github.com/velolib/vinth.git
cd vinth
go build -o vinth .
```

Windows:

```powershell
go build -o vinth.exe .
```

## Usage

General help:

```bash
vinth help
```

Command-specific help:

```bash
vinth <command> --help
```

### Quick start

```bash
vinth create
vinth add sodium fabric-api iris
vinth deps --add
vinth sync
vinth list
```

## Commands (with examples)

### `vinth create`

Initialize or overwrite `vinth.lock.json` via interactive wizard.

```bash
vinth create
```

### `vinth add [mod-identifiers...]`

Add one or more mods by slug (default) or Modrinth project ID.

```bash
# Add by slug
vinth add sodium fabric-api iris

# Add by Modrinth project ID
vinth add --id AANobbMI P7dR8mSH
vinth add --modrinth-id AANobbMI
```

### `vinth remove [mod-identifiers...]`

Remove mods by slug, by project ID, or interactively (no args).

```bash
# Remove by slug
vinth remove sodium fabric-api

# Remove by Modrinth project ID
vinth remove --id AANobbMI P7dR8mSH
vinth remove --modrinth-id AANobbMI

# Interactive mode
vinth remove
```

### `vinth list`

Display all tracked mods in the lockfile.

```bash
vinth list
```

### `vinth deps`

Check required dependencies.

```bash
vinth deps
```

### `vinth deps --add`

Auto-add missing required dependencies.

```bash
vinth deps --add
```

### `vinth upgrade [mod-slugs...]`

Upgrade all mods or specific mods.

```bash
# Upgrade all
vinth upgrade

# Upgrade specific mods
vinth upgrade sodium
vinth upgrade sodium lithium fabric-api
```

### `vinth edit`

Interactively change Minecraft target version and loader, preview compatibility, and apply changes.

```bash
vinth edit
```

### `vinth sync`

Sync current directory to match lockfile.

`vinth sync` also supports local mods:

- Put custom `.jar` files in `local/` (same directory as `vinth.lock.json`).
- On every sync, vinth copies `local/*.jar` (non-recursive) into the current directory.
- These local mod filenames are excluded from sync prune removal.
- Local mods are copied as-is and are not hash/version checked by vinth.

```bash
# Sync and prune untracked jar files (default behavior)
vinth sync

# Sync without pruning
vinth sync --no-prune

# Sync and auto-confirm prune prompt
vinth sync --yes
```

### `vinth clean`

Remove orphaned `.jar` files not tracked by lockfile.

```bash
# Preview only
vinth clean --dry-run

# Delete with confirmation
vinth clean

# Delete without confirmation
vinth clean --yes
```

### `vinth completion [bash|fish|powershell|zsh]`

Generate shell completion scripts.

```bash
vinth completion bash
vinth completion fish
vinth completion powershell
vinth completion zsh
```

PowerShell profile example:

```powershell
vinth completion powershell | Out-String | Invoke-Expression
```

## Lockfile

vinth stores state in `vinth.lock.json`.

At a high level it includes:

- Minecraft version
- Mod loader
- Mod entries (project/version IDs, download URL, file metadata, hash)

Treat this file as source-controlled project state.

Note: mods from `local/` are intentionally not stored in `vinth.lock.json`; they are local overrides/additions copied during `vinth sync`.

## Reproducible Workflow Suggestion

1. `vinth create`
2. `vinth add ...`
3. `vinth deps --add`
4. `vinth sync`
5. Commit `vinth.lock.json`

Anyone else can then run:

1. `vinth sync`

to materialize the same tracked mods.