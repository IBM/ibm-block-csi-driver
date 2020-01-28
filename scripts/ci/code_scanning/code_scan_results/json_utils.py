from json import load, dump


def get_deserialized_json_data(data_file_path):
    with open(data_file_path, "r") as data_file:
        data = load(data_file)
    return data


def serialize_json_data(data_file_path, data):
    with open(data_file_path, "w") as data_file:
        dump(data, data_file, indent=4)
