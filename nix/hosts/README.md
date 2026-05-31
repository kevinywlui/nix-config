# Hosts

Each subdirectory here is one machine's NixOS entrypoint. A host directory holds three
files:

- **`default.nix`** — the host entrypoint: its `imports` list, machine-specific settings,
  and any host-only services.
- **`hardware.nix`** — the generated hardware scan (CPU/kernel modules, filesystems).
- **`secrets.yaml`** — this host's `sops`-encrypted secrets.

## What runs on a host

**The `imports = [ ... ]` block in a host's `default.nix` is the source of truth** for
which modules that host runs — read it directly rather than trusting a table here (a
hand-maintained list would only drift). The two hosts deliberately differ: fw13 pulls in
the desktop and dev profiles; t480 pulls in the server services instead.

What belongs in the host file vs. a shared module follows the placement rule in
`../README.md`: **machine-specific** config (hardware quirks, hostname, host-only
services) stays in the host; **reusable** config moves to a module under `../modules/`.

## `common/`

`common/disko.nix` is the LUKS2 + Btrfs disk layout (subvolumes `@`, `@home`, `@nix`
on `/dev/nvme0n1`). It's wired into every host through the flake's `mkHost` rather than
imported per-host. It isn't a NixOS module — it configures the `disko` flake input — so it
sits next to the host dirs rather than under `../modules/`.

If a future host's disk layout diverges, push this file down to per-host
(`hosts/<name>/disko.nix`) and drop the entry from `common/`. `common/` is for things
**every** host imports verbatim; the moment one host doesn't, it stops being common.

## Adding a host

1. **Create `hosts/<name>/`** with a `default.nix` that imports the modules you want plus
   `./hardware.nix`.
2. **Generate hardware config** on the target: `nixos-generate-config --no-fstab --root /mnt`,
   then copy `hardware-configuration.nix` to `hosts/<name>/hardware.nix`.
3. **Register the host** in `../../flake.nix`: add a `mkHost` entry with the appropriate
   `nixos-hardware` module and `hostModule = ./nix/hosts/<name>`.
4. **Wire up secrets** (the easy step to get wrong):
   - Derive the host's age key from its SSH host key: `ssh-to-age` on
     `/etc/ssh/ssh_host_ed25519_key.pub`.
   - Add that public key to `../../.sops.yaml` so the new `secrets.yaml` can be encrypted to
     it, then `sops nix/hosts/<name>/secrets.yaml`.
   - **Invariant:** every host decrypts only its *own* `secrets.yaml`, via its SSH host
     key, at activation time. The personal `klui` age key is also listed in `.sops.yaml`
     and can decrypt any host's secrets for manual editing and recovery.

The complete secrets workflow is in `../../AGENTS.md` (Secrets Management).
