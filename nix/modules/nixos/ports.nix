# Shared registry of service ports (my.ports.*), imported by the service modules in
# this directory so every port assignment lives in one place.
{ lib, ... }:

{
  options.my.ports = {
    paperless = lib.mkOption {
      type = lib.types.port;
      default = 28981;
      description = "Paperless-ngx document management";
    };
    statusPage = lib.mkOption {
      type = lib.types.port;
      default = 8088;
      description = "Static status page web server";
    };
    gtd = lib.mkOption {
      type = lib.types.port;
      default = 8730;
      description = "Guided-GTD web server (todo.txt); fronted by tailscale serve";
    };
  };
}
