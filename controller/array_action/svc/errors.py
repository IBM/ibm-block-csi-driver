import re
from .. import errors

error_code_pattern = re.compile(r'CMMVC[0-9]+[EW]')
object_exists = "CMMVC6035E"
object_not_exists = "CMMVC5753E"
invalid_name = "CMMVC6527E"
invalid_data = "CMMVC5711E"
iscsi_port_already_assigned = "CMMVC6578E"
fc_port_already_assigned = "CMMVC5867E"


class InvalidData(errors.BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)


class ObjectAlreadyExists(errors.BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)


class ObjectNotFound(errors.BaseArrayActionException):

    def __init__(self, msg):
        self.message = "{0}".format(msg)


def extract_error_code(error_str):
    codes = error_code_pattern.findall(error_str)
    if not codes:
        return ""
    else:
        return codes[0]


def is_warning_message(error_code):
    if error_code[-1] == 'W':
        return True
    return False


def is_object_existing(error_code):
    return error_code == object_exists


def is_object_not_existing(error_code):
    return error_code == object_not_exists


def is_name_invalid(error_code):
    return error_code == invalid_name


def is_data_invalid(error_code):
    return error_code == invalid_data


def is_iscsi_port_already_assigned(error_code):
    return error_code == iscsi_port_already_assigned


def is_fc_port_already_assigned(error_code):
    return error_code == fc_port_already_assigned


def is_host_port_already_assigned(error_code):
    return is_iscsi_port_already_assigned(error_code) or is_fc_port_already_assigned(error_code)


class ErrorPreprocessor(object):
    """
    pre-process a CommandExecutionError or CLIFailureError
    """

    def __init__(self, error, logger=None, skip_not_existing_object=False, skip_existing_object=False):
        self.error = error
        self.logger = logger
        self.skip_not_existing_object = skip_not_existing_object
        self.skip_existing_object = skip_existing_object

    def process(self):
        """
        process the error

        :return: if_it_is_an_error, error_code, origin_error
        :rtype: tuple
        """

        error_code = extract_error_code(str(self.error))
        if not error_code:
            return False, "", self.error

        if is_warning_message(error_code):
            if self.logger:
                self.logger.warning("action succeeded with warning: {}".format(self.error))
            return False, error_code, self.error

        if is_object_existing(error_code):
            if self.skip_existing_object:
                if self.logger:
                    self.logger.warning("action succeeded with warning: {}".format(self.error))
                return False, error_code, self.error
            else:
                raise ObjectAlreadyExists(str(self.error))

        if is_object_not_existing(error_code):
            if self.skip_not_existing_object:
                if self.logger:
                    self.logger.warning("action succeeded with warning: {}".format(self.error))
                return False, error_code, self.error
            else:
                raise ObjectNotFound(str(self.error))

        if is_name_invalid(error_code):
            raise errors.IllegalObjectName(str(self.error))

        if is_data_invalid(error_code):
            raise InvalidData(str(self.error))

        if self.logger:
            self.logger.error("action failed with error: {}".format(self.error))
        return True, error_code, self.error
