#!/bin/sh
# Default local full-stack entry: includes managed data and Web, no profiles.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec python3 "$ROOT/scripts/compose-managed.py" "$@"
