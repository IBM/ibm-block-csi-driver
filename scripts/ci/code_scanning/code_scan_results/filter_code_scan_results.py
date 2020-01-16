from os import path
from sys import argv, stderr, exit
from re import sub
import json_utils as utils


def main():
    output_path, target_name = tuple(argv[1:])
    tools = {"csi-controller-code-scan": "bandit", "csi-controller-dep-code-scan": "safety",
             "csi-node-code-scan": "gosec", "csi-node-dep-code-scan": "gosec"}

    code_scan_file_path = path.join(output_path, "{}.json".format(target_name))
    code_scan_res = utils.get_deserialized_json_data(code_scan_file_path)

    if not code_scan_res:
        print("No results were found.")
    else:
        _filter_code_scan_res(tools[target_name], code_scan_res)

    filtered_code_scan_file_path = path.join(output_path, "filtered-{}.json".format(target_name))
    utils.serialize_json_data(filtered_code_scan_file_path, code_scan_res)


def _filter_code_scan_res(tool, code_scan_res):
    if tool == "safety":
        _remove_inner_fields_list(code_scan_res, inner_field_to_remove_index=2)
    elif tool == "bandit":
        _remove_fields_dict(code_scan_res, fields_to_remove_keys=("metrics", "generated_at", "errors"))
        _remove_inner_fields_dict(code_scan_res, key_for_inner_dicts="results",
                                  inner_fields_to_remove_keys=("line_number", "line_range", "more_info"))
        _remove_line_numbers_bandit(code_scan_res, key_for_inner_dicts="results")
    else:
        _remove_fields_dict(code_scan_res, fields_to_remove_keys=("Golang errors", "Stats"))
        _remove_inner_fields_dict(code_scan_res, key_for_inner_dicts="Issues",
                                  inner_fields_to_remove_keys=("cwe", "line"))


def _remove_inner_fields_list(code_scan_res, inner_field_to_remove_index):
    try:
        for inner_list in code_scan_res:
            del inner_list[inner_field_to_remove_index]
    except KeyError as err:
        stderr.write("Error: KeyError occurred at {}.\n".format(err))
        exit(1)


def _remove_fields_dict(code_scan_res, fields_to_remove_keys):
    try:
        for field_to_remove_key in fields_to_remove_keys:
            del code_scan_res[field_to_remove_key]
    except KeyError as err:
        stderr.write("Error: KeyError occurred at {}.\n".format(err))
        exit(1)


def _remove_inner_fields_dict(code_scan_res, key_for_inner_dicts, inner_fields_to_remove_keys):
    try:
        for inner_dict in code_scan_res[key_for_inner_dicts]:
            for inner_field_to_remove_key in inner_fields_to_remove_keys:
                del inner_dict[inner_field_to_remove_key]
    except KeyError as err:
        stderr.write("Error: KeyError occurred at {}.\n".format(err))
        exit(1)


def _remove_line_numbers_bandit(code_scan_res, key_for_inner_dicts):
    try:
        for inner_dict in code_scan_res[key_for_inner_dicts]:
            inner_dict["code"] = sub(r"(^\d+)|(\n\d+)|(\n$)", " ", inner_dict["code"])
    except KeyError as err:
        stderr.write("Error: KeyError occurred at {}.\n".format(err))
        exit(1)


if __name__ == "__main__":
    main()
