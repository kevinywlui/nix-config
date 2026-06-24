# Overlay: the in-repo `gtd` package — the guided-GTD web server (`gtd-server`)
# and its CLI client (`gtd`), built from apps/gtd. Source lives in this repo
# (not a flake input) so there is no extra supply-chain surface, and the module
# is deliberately stdlib-only, which is why `vendorHash = null` is correct (no
# external dependency closure to pin). buildGoModule builds every `main` package
# under cmd/, producing both binaries.
_inputs: final: _prev: {
  gtd = final.buildGoModule {
    pname = "gtd";
    version = "0.1.0";
    src = ../../apps/gtd;
    vendorHash = null;
    meta = {
      description = "Guided Getting Things Done over todo.txt (web server + CLI)";
      mainProgram = "gtd";
    };
  };
}
