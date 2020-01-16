from os import path
from sys import argv, exit
from deepdiff import DeepDiff
import json_utils as utils


def main():
    output_path, baseline_path, target_name = tuple(argv[1:])

    filtered_file_path = path.join(output_path, "filtered-{}.json".format(target_name))
    baseline_file_path = path.join(baseline_path, "baseline-{}.json".format(target_name))
    templine_file_path = path.join(baseline_path, "templine-{}.json".format(target_name))
    filtered_baseline_diff, filtered_templine_diff = _get_diffs_results(filtered_file_path, baseline_file_path,
                                                                        templine_file_path)

    filtered_baseline_diff_file_path = path.join(output_path, "filtered-baseline-diff-{}.json".format(target_name))
    filtered_templine_diff_file_path = path.join(output_path, "filtered-templine-diff-{}.json".format(target_name))
    _check_diffs_results(filtered_baseline_diff_file_path, filtered_templine_diff_file_path, filtered_baseline_diff,
                         filtered_templine_diff)


def _get_diffs_results(filtered_file_path, baseline_file_path, templine_file_path):
    filtered_res = utils.get_deserialized_json_data(filtered_file_path)
    baseline_res = utils.get_deserialized_json_data(baseline_file_path)
    templine_res = utils.get_deserialized_json_data(templine_file_path)
    return (DeepDiff(filtered_res, baseline_res, ignore_order=True),
            DeepDiff(filtered_res, templine_res, ignore_order=True))


def _check_diffs_results(filtered_baseline_diff_file_path, filtered_templine_diff_file_path, filtered_baseline_diff,
                         filtered_templine_diff):
    if filtered_baseline_diff and filtered_templine_diff:
        _serialize_diffs_results(filtered_baseline_diff_file_path, filtered_baseline_diff,
                                 filtered_templine_diff_file_path, filtered_templine_diff)
        exit(1)
    elif not filtered_baseline_diff and filtered_templine_diff:
        _serialize_diffs_results(filtered_templine_diff_file_path, filtered_templine_diff)
        exit(1)
    elif filtered_baseline_diff and not filtered_templine_diff:
        _serialize_diffs_results(filtered_baseline_diff_file_path, filtered_baseline_diff)
        exit(2)
    else:
        exit(0)


def _serialize_diffs_results(*args):
    index = 0
    while index < len(args):
        utils.serialize_json_data(data_file_path=args[index], data=args[index + 1])
        index += 2


if __name__ == "__main__":
    main()
