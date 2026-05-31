{ config, inputs, ... }:

# Sets system.nixos.label to "<hostname>-YYYYMMDD-HHmm-<githash>" in
# America/Los_Angeles time, derived from the flake's lastModifiedDate.
# Pure Nix arithmetic — no IFD, no external tools.
#
# Imported by profiles/core.nix so every host inherits it automatically.
let
  hostname = config.networking.hostName;
  rev = inputs.self.rev or inputs.self.dirtyRev or "dirty";
  date = inputs.self.lastModifiedDate or "19700101000000";

  # builtins.fromJSON rejects leading zeros; strip them for 2-digit fields
  toInt = s: builtins.fromJSON (if builtins.substring 0 1 s == "0" then builtins.substring 1 1 s else s);
  year = builtins.fromJSON (builtins.substring 0 4 date);
  month = toInt (builtins.substring 4 2 date);
  day = toInt (builtins.substring 6 2 date);
  hour = toInt (builtins.substring 8 2 date);
  minute = toInt (builtins.substring 10 2 date);

  # Zeller's congruence: returns 0=Sun, 1=Mon, ..., 6=Sat
  dayOfWeek = y: m: d:
    let
      y' = if m < 3 then y - 1 else y;
      m' = if m < 3 then m + 12 else m;
      k = builtins.mod y' 100;
      j = y' / 100;
      h = builtins.mod (d + (13 * (m' + 1) / 5) + k + (k / 4) + (j / 4) - 2 * j) 7;
    in
    builtins.mod (h + 6) 7;

  # Day of month for the nth occurrence of weekday (0=Sun) in year y, month m
  nthWeekday = y: m: weekday: n:
    let daysToFirst = builtins.mod (weekday - dayOfWeek y m 1 + 7) 7;
    in 1 + daysToFirst + (n - 1) * 7;

  # DST rules for America/Los_Angeles:
  #   PDT (UTC-7): 2nd Sunday in March at 02:00 PST (= 10:00 UTC)
  #   PST (UTC-8): 1st Sunday in November at 02:00 PDT (= 09:00 UTC)
  dstStartDay = nthWeekday year 3 0 2;
  dstEndDay = nthWeekday year 11 0 1;

  afterDstStart =
    month > 3
    || (month == 3 && day > dstStartDay)
    || (month == 3 && day == dstStartDay && hour >= 10);

  beforeDstEnd =
    month < 11
    || (month == 11 && day < dstEndDay)
    || (month == 11 && day == dstEndDay && hour < 9);

  offset = if afterDstStart && beforeDstEnd then -7 else -8;

  # Apply offset, rolling back across midnight if needed
  isLeap = y: (builtins.mod y 4 == 0 && builtins.mod y 100 != 0) || (builtins.mod y 400 == 0);
  daysInMonth = y: m:
    if m == 2 then (if isLeap y then 29 else 28)
    else if builtins.elem m [ 4 6 9 11 ] then 30
    else 31;

  rawHour = hour + offset;
  prevM = if month == 1 then 12 else month - 1;
  prevY = if month == 1 then year - 1 else year;
  laHour = if rawHour < 0 then rawHour + 24 else rawHour;
  laDay = if rawHour < 0 then daysInMonth prevY prevM else day;
  laMonth = if rawHour < 0 then prevM else month;
  laYear = if rawHour < 0 then prevY else year;

  pad2 = n: let s = builtins.toString n; in if builtins.stringLength s == 1 then "0" + s else s;
  laDate = "${builtins.toString laYear}${pad2 laMonth}${pad2 laDay}-${pad2 laHour}${pad2 minute}";
in
{
  system.nixos.label = "${hostname}-${laDate}-${builtins.substring 0 7 rev}";
}
