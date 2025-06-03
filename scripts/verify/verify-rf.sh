#!/bin/bash
set -euo pipefail

ROOTDIR=$(git rev-parse --show-toplevel)

RF_VENV="${ROOTDIR}/_output/robotenv"
"${ROOTDIR}/scripts/fetch_tools.sh" robotframework

cd "${ROOTDIR}/test"

# Configured robocop rules:
# https://robocop.readthedocs.io/en/stable/rules.html#too-long-test-case-w0504
# https://robocop.readthedocs.io/en/stable/rules.html#too-many-calls-in-test-case-w0505

set -x
"${RF_VENV}/bin/robocop" \
    --exclude 1015 \
    --configure line-too-long.line_length=200
    # --configure max_len=40 \
    # --configure max_calls=20 \
    # --configure line_length=200 \
    # --configure max_lines=1000

"${RF_VENV}/bin/robotidy" --check --diff .
