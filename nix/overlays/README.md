# Overlays

Nixpkgs overlays applied to every host. Wired once at the flake layer
(`flake.nix` → `nixpkgsConfig.nixpkgs.overlays`), **not** from inside a
module — that way a host that skips a profile still gets `pkgs.unstable`.

## Contract

`default.nix` evaluates to `inputs: final: prev: { ... }` — a function that
takes the flake `inputs` and returns a single overlay function.

```nix
# flake.nix
nixpkgs.overlays = [ (import ./nix/overlays inputs) ];
```

## Adding another overlay

1. Write `nix/overlays/<name>.nix` as `inputs: final: prev: { … }`.
2. Merge it into `default.nix`:
   ```nix
   inputs: final: prev:
     (import ./<name>.nix inputs final prev) //
     { unstable = …; }
   ```

Keep one function per file; aggregate in `default.nix`.
