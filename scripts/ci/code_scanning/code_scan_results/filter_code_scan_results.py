from os import path
from sys import argv
from re import sub
import json_utils as utils


def main():
    output_path, target_name = tuple(argv[1:])
    tools = {"csi-controller-code-scan": "bandit", "csi-controller-dep-code-scan": "safety",
             "csi-node-code-scan": "gosec", "csi-node-dep-code-scan": "gosec"}

    code_scan_res = get_code_scan_res(output_path, target_name)
    if not code_scan_res: 
        print("No results were found.")
    else:
        _filter_code_scan_res(tools[target_name], code_scan_res)
    serialize_filtered_res(output_path, target_name, code_scan_res)


def get_code_scan_res(output_path, target_name):
    code_scan_file_path = path.join(output_path, "{}.json".format(target_name))
    return utils.get_deserialized_json_data(code_scan_file_path)


def _filter_code_scan_res(tool, code_scan_res):
    if tool == "safety":
        _filter_code_scan_res_safety(code_scan_res)
    elif tool == "bandit":
        _filter_code_scan_res_bandit(code_scan_res)
    else:
        filter_code_scan_res_gosec(code_scan_res)


def _filter_code_scan_res_safety(code_scan_res):
    _remove_inner_fields_list(code_scan_res, index_to_remove=2)


def _filter_code_scan_res_bandit(code_scan_res):
    _remove_fields_dict(code_scan_res, keys_to_remove=("metrics", "generated_at", "errors"))
    _remove_inner_fields_dict(code_scan_res["results"], keys_to_remove=("line_number", "line_range", "more_info"))
    _remove_line_numbers_bandit(code_scan_res["results"])


def filter_code_scan_res_gosec(code_scan_res):
    _remove_fields_dict(code_scan_res, keys_to_remove=("Golang errors", "Stats"))
    _remove_inner_fields_dict(code_scan_res["Issues"], keys_to_remove=("cwe", "line"))


def _remove_inner_fields_list(code_scan_res, index_to_remove):
    for inner_list in code_scan_res:
        del inner_list[index_to_remove]


def _remove_inner_fields_dict(inner_dicts, keys_to_remove):
    for inner_dict in inner_dicts:
        _remove_fields_dict(inner_dict, keys_to_remove)


def _remove_fields_dict(code_scan_res, keys_to_remove):
    for key_to_remove in keys_to_remove:
        del code_scan_res[key_to_remove]


def _remove_line_numbers_bandit(inner_dicts):
    for inner_dict in inner_dicts:
        inner_dict["code"] = sub(r"(^\d+)|(\n\d+)|(\n$)", "", inner_dict["code"])


def serialize_filtered_res(output_path, target_name, code_scan_res):
    filtered_code_scan_file_path = path.join(output_path, "filtered-{}.json".format(target_name))
    utils.serialize_json_data(filtered_code_scan_file_path, code_scan_res)


if __name__ == "__main__":
    main()
