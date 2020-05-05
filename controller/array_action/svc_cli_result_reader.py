from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()


class SVCListResultsReader:
    ID_PARAM_NAME = "id"

    def __init__(self, hosts_raw_list_as_string):
        self._hosts_raw_list = hosts_raw_list_as_string.split("\n")
        self._current_index = 0
        self._next_object_id = None
        self._init_first_object_id()

    def _init_first_object_id(self):
        while not self._next_object_id and self._current_index < len(self._hosts_raw_list):
            line = self._hosts_raw_list[self._current_index].strip()
            self._current_index += 1
            if line:
                param_name, param_value = self._parse_param(line)
                if param_name == SVCListResultsReader.ID_PARAM_NAME:
                    self._next_object_id = param_value
                    self._current_index -= 1
                else:
                    raise StopIteration(
                        "First element is {0}. Expected {1}".format(line, SVCListResultsReader.ID_PARAM_NAME))

    def __iter__(self):
        return self

    def __next__(self):
        if not self._has_next():
            raise StopIteration
        res = SVCListResultsElement()
        res.add(SVCListResultsReader.ID_PARAM_NAME, self._next_object_id)
        self._current_index += 1
        self._next_object_id = None
        while self._current_index < len(self._hosts_raw_list):
            line = self._hosts_raw_list[self._current_index].strip()
            self._current_index += 1
            if not line:
                continue
            param_name, param_value = self._parse_param(line)
            if param_name == SVCListResultsReader.ID_PARAM_NAME:
                self._next_object_id = param_value
                self._current_index -= 1
                return res
            res.add(param_name, param_value)
        return res

    def _parse_param(self, line):
        splitted_line = line.split()
        name = splitted_line[0]
        value = line[len(name):].strip() if len(splitted_line) > 1 else ""
        return name, value

    def _has_next(self):
        return self._next_object_id


class SVCListResultsElement:
    def __init__(self):
        self._dict = {}

    def get(self, name, default_value=None):
        return self._dict.get(name, default_value)

    def get_as_list(self, name):
        if name in self._dict:
            value = self._dict[name]
            return value if isinstance(value, list) else [value]
        else:
            return []

    def add(self, name, value):
        if name in self._dict:
            curr_val = self._dict[name]
            if isinstance(curr_val, list):
                curr_val.append(value)
            else:
                new_val = [curr_val, value]
                self._dict[name] = new_val
        else:
            self._dict[name] = value

    def __str__(self):
        return self._dict.__str__()
