#!/bin/bash -x

python code_scan_results/filter_code_scan_results.py ${CON_OUTPUT_PATH} ${TARGET_NAME} || exit $?
python code_scan_results/diff_code_scan_results.py ${CON_OUTPUT_PATH} ${CON_BASELINE_PATH} ${TARGET_NAME} || exit $?
