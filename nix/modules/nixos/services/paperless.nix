{ config, ... }:

{
  imports = [ ../ports.nix ];
  services.paperless = {
    enable = true;
    address = "127.0.0.1";
    port = config.my.ports.paperless;
    dataDir = "/var/lib/paperless";
    consumptionDir = "/var/lib/paperless/consume";
    consumptionDirIsPublic = true;
    passwordFile = config.sops.secrets.paperless-password.path;
    settings = {
      PAPERLESS_OCR_LANGUAGE = "eng";
    };
  };
}
