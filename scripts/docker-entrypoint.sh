#!/bin/sh
set -eu

# Preserve explicit CLI commands such as "-command import" or "-command user".
if [ "$#" -gt 0 ]; then
  exec /app/pulse "$@"
fi

if [ ! -f "${DB_PATH:-/app/storage/pulse.db}" ]; then
  # Only a new volume receives historical answers and demonstration accounts.
  /app/pulse -command demo
else
  # Existing answers, passwords and analyses are not imported or replaced.
  /app/pulse -command migrate
fi

exec /app/pulse
