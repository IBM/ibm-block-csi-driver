#!/bin/bash -x

python code_scan_results/filter_code_scan_results.py ${CONTAINER_OUTPUT_PATH} ${TARGET_NAME} || exit
python code_scan_results/diff_code_scan_results.py ${CONTAINER_OUTPUT_PATH} ${CONTAINER_BASELINE_PATH} ${TARGET_NAME} || exit
