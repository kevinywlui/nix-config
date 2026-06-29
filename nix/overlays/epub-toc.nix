# Overlay: the in-repo `epub-toc` package — a CLI that adds a table of contents
# to EPUB files that are missing one (apps/epub-toc). Like `gtd`, the source
# lives in this repo (not a flake input) and is deliberately standard-library
# only, so there is no external dependency closure to pin — the derivation just
# installs the script and points its shebang at the pinned python3. Tests run as
# the derivation's checkPhase, so a broken tool fails `nh os build`.
_inputs: final: _prev: {
  epub-toc = final.stdenvNoCC.mkDerivation {
    pname = "epub-toc";
    version = "0.1.0";
    src = ../../apps/epub-toc;

    nativeBuildInputs = [ final.python3 ];

    dontConfigure = true;
    dontBuild = true;

    doCheck = true;
    checkPhase = ''
      runHook preCheck
      ${final.python3.interpreter} -m unittest discover -s . -v
      runHook postCheck
    '';

    installPhase = ''
      runHook preInstall
      install -Dm755 epub_toc.py "$out/bin/epub-toc"
      patchShebangs "$out/bin/epub-toc"
      runHook postInstall
    '';

    meta = {
      description = "Add a table of contents to EPUBs that are missing one";
      mainProgram = "epub-toc";
    };
  };
}
