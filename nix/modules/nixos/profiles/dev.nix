# Shared headless dev/build toolchain — imported wholesale by every host that
# builds code (fw13 desktop, t480 headless server). Deliberately holds only the
# common denominator: CLI compilers, linters, and build tools that run without a
# display. GUI-only dev IDEs (android-studio, arduino-ide) and physical-flashing
# tools (esptool, platformio) stay in fw13's home.packages, so the headless t480
# can import this profile cleanly. Android *SDK* provisioning is per-host (t480
# pins it via androidenv; fw13 uses android-studio's bundled SDK manager).
{ pkgs, ... }:

{
  environment.systemPackages =
    let
      compilerTools = with pkgs; [
        clang
        clang-tools
        jdk21
        nodejs_latest
        python3
      ];

      linters = with pkgs; [
        gitleaks
        nixpkgs-fmt
        shellcheck
        shfmt
        stylua
      ];

      buildTools = with pkgs; [
        android-tools
        arduino-cli # headless ESP32/Arduino sketch builds (round-earth-project's .ino)
        gradle
        tree-sitter
      ];
    in
    compilerTools ++ linters ++ buildTools;

  programs.nix-ld = {
    enable = true;
    libraries = with pkgs; [
      stdenv.cc.cc.lib
      zlib
      glibc
    ];
  };

  users.users.klui.extraGroups = [ "kvm" ];
}
