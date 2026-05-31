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
        gradle
        tree-sitter
      ];

      ides = with pkgs; [
        android-studio
      ];
    in
    compilerTools ++ linters ++ buildTools ++ ides;

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
