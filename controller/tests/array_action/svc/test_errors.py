from unittest import TestCase
from controller.array_action.svc import errors
from controller.array_action.errors import IllegalObjectName


errors_warning = "CLI failure. Return code is 1. Error message is \"b'CMMVC1234W it is a warning.\n'\""
errors_object_exists = "CLI failure. Return code is 1. Error message is \"b'CMMVC6035E The action failed as the object already exists.\n'\""  # noqa
errors_object_not_exists = "CLI failure. Return code is 1. Error message is \"b'CMMVC5753E The specified object does not exist or is not a suitable candidate.\n'\""  # noqa
errors_other_error = "CLI failure. Return code is 1. Error message is \"b'CMMVC6099E it is an error\n'\""
errors_invalid_name = "CLI failure. Return code is 1. Error message is \"b'CMMVC6527E invalid name\n'\""
errors_invalid_data = "CLI failure. Return code is 1. Error message is \"b'CMMVC5711E invalid data\n'\""

class TestErrors(TestCase):

    def test_is_warning_message(self):
        error_code = errors.extract_error_code(errors_warning)
        self.assertTrue(errors.is_warning_message(error_code))

        error_code = errors.extract_error_code(errors_other_error)
        self.assertFalse(errors.is_warning_message(error_code))

    def test_is_object_exists(self):
        error_code = errors.extract_error_code(errors_object_exists)
        self.assertTrue(errors.is_object_existing(error_code))

        error_code = errors.extract_error_code(errors_other_error)
        self.assertFalse(errors.is_object_existing(error_code))

    def test_is_object_not_exists(self):
        error_code = errors.extract_error_code(errors_object_not_exists)
        self.assertTrue(errors.is_object_not_existing(error_code))

        error_code = errors.extract_error_code(errors_other_error)
        self.assertFalse(errors.is_object_not_existing(error_code))

    def test_process_warning(self):
        is_error, _, _ = errors.ErrorPreprocessor(errors_warning, logger=None).process()
        self.assertEqual(is_error, False)

    def test_process_other_error(self):
        is_error, _, _ = errors.ErrorPreprocessor(errors_other_error, logger=None).process()
        self.assertEqual(is_error, True)

    def test_process_object_exists(self):
        with self.assertRaises(errors.ObjectAlreadyExists):
            errors.ErrorPreprocessor(errors_object_exists, logger=None).process()

    def test_process_object_not_exists(self):
        with self.assertRaises(errors.ObjectNotFound):
            errors.ErrorPreprocessor(errors_object_not_exists, logger=None).process()

    def test_process_skip_not_existing_object(self):
        is_error, _, _ = errors.ErrorPreprocessor(errors_object_not_exists, logger=None, skip_not_existing_object=True).process()
        self.assertEqual(is_error, False)

    def test_process_skip_existing_object(self):
        is_error, _, _ = errors.ErrorPreprocessor(errors_object_exists, logger=None, skip_existing_object=True).process()
        self.assertEqual(is_error, False)

    def test_process_invalid_name(self):
        with self.assertRaises(IllegalObjectName):
            errors.ErrorPreprocessor(errors_invalid_name, logger=None).process()

    def test_process_invalid_data(self):
        with self.assertRaises(errors.InvalidData):
            errors.ErrorPreprocessor(errors_invalid_data, logger=None).process()
