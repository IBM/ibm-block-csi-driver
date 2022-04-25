import os.path

import yaml
from munch import DefaultMunch

_this_file_path = os.path.abspath(os.path.dirname(__file__))
_config_path = os.path.join(_this_file_path, "../../common/config.yaml")

with open(_config_path, 'r') as yamlfile:
    _cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
config = DefaultMunch.fromDict(_cfg)
