from controller.common.csi_logger import get_stdout_logger

UTF_8 = "utf-8"

logger = get_stdout_logger()


class SVCListResultsReader:
    ID_PARAM_NAME = "id"

    def __init__(self, raw_command_res):
        self._hosts_raw_list = self._get_command_res_output(raw_command_res)
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

    def has_next(self):
        return self._next_object_id

    def get_next(self):
        if not self.has_next():
            raise StopIteration("No more entries exist")
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
        if len(splitted_line) == 1:
            value = ""
        else:
            value = splitted_line[1]
        return name, value

    def _get_command_res_output(self, raw_command_res):
        if not raw_command_res:
            logger.warn("Command returned empty response")
            return ""
        res_output_as_bytes = raw_command_res[0]
        res_output_as_string = _bytes_to_string(res_output_as_bytes).strip()
        if len(raw_command_res) == 2:
            res_errors_as_bytes = raw_command_res[1]
            res_errors_as_string = _bytes_to_string(res_errors_as_bytes).strip()
            if res_errors_as_string:
                logger.warn("Errors returned from in cli command {0}".format(res_errors_as_string))
        return res_output_as_string


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


def _bytes_to_string(input_as_bytes):
    return input_as_bytes.decode(UTF_8)
