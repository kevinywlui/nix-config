# CLI tools surfaced by nix/scripts/tool-usage-report.py as frequent ad-hoc
# pulls — packages that AI agents (and the user) kept reaching for via
# `nix-shell -p X` / `nix run nixpkgs#X` often enough to be worth declaring.
# Kept in its own profile (rather than core's cliTools) so provenance stays
# explicit and the set is easy to audit against the weekly report and prune as
# usage shifts. Imported wholesale by every host — agents run on both. Holds
# only display-free CLI tools, like dev.nix, so the headless t480 imports it
# cleanly.
{ pkgs, ... }:

{
  environment.systemPackages = with pkgs; [
    poppler-utils # pdftotext / pdfinfo — reading PDFs (107 ad-hoc pulls)
    imagemagick # convert / identify — image inspection & conversion
    sqlite # sqlite3 — querying app/state databases
  ];
}
