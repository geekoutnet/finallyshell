#!/bin/sh
# Sets the test machine's passwords from the environment (never baked into the image).
set -eu
# Read each password on stdin instead of passing it as a command-line argument.
printf 'root:%s\n' "$ROOT_PW" | chpasswd
printf 'tester:%s\n' "$TESTER_PW" | chpasswd
