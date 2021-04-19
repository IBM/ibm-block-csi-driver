import controller.array_action.errors as array_errors
from controller.common.csi_logger import get_stdout_logger

logger = get_stdout_logger()

ID_PARAM_NAME = "id"


class SVCListResultsReader:
    """
    Iterable object used to read raw command response from SVC array.
    Each object in response is translated to SVCListResultsElement.
    Input is received as string which represents '\n'-separated list of returned lines from output.
    Line with param 'id' (e.g. 'id 1') is recognized as first line of object ans used as separator between objects
    (e.g. in input "id 1\nname n3<new line>id 2<new line>name n2" first object starts with line 'id 1'
    and ends with 'id 2')
    """

    def __init__(self, hosts_raw_list_as_string):
        self._hosts_raw_list = hosts_raw_list_as_string.splitlines()
        self._current_index = 0
        self._next_object_id = None
        self._init_first_object_id()

    def _init_first_object_id(self):
        """
        Set _next_object_id to id of the first object and _current_index to point to next line

        Raises:
            InvalidCliResponseError
        """
        while not self._next_object_id and self._current_index < len(self._hosts_raw_list):
            line = self._hosts_raw_list[self._current_index].strip()
            self._current_index += 1
            if line:
                param_name, _, param_value = line.partition(' ')
                param_value = param_value.strip()
                if param_name == ID_PARAM_NAME:
                    self._next_object_id = param_value
                else:
                    raise array_errors.InvalidCliResponseError(
                        "First param is '{0}'. Expected param name '{1}'".format(line, ID_PARAM_NAME))

    def __iter__(self):
        return self

    def __next__(self):
        """
        Assumed self._current_index points to the line after line 'id <id>' of the next object

        Returns:
            Next object as SVCListResultsElement.
        Raises:
            StopIteration
        """

        if not self._has_next():
            raise StopIteration

        res = SVCListResultsElement()
        res.add(ID_PARAM_NAME, self._next_object_id)
        self._next_object_id = None
        while self._current_index < len(self._hosts_raw_list):
            line = self._hosts_raw_list[self._current_index].strip()
            self._current_index += 1
            if not line:
                continue
            param_name, _, param_value = line.partition(' ')
            param_value = param_value.strip()
            if param_name == ID_PARAM_NAME:
                self._next_object_id = param_value
                return res
            res.add(param_name, param_value)
        return res

    def _has_next(self):
        return self._next_object_id


class SVCListResultsElement:
    """
    Single parsed object returned from SVC list command
    """

    def __init__(self):
        self._dict = {}

    def get(self, name, default_value=None):
        return self._dict.get(name, default_value)

    def get_as_list(self, name):
        if name in self._dict:
            value = self._dict[name]
            return value if isinstance(value, list) else [value]
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
