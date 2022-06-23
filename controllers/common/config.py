import os.path

import yaml
from munch import DefaultMunch

_this_file_path = os.path.abspath(os.path.dirname(__file__))
_config_path = os.path.join(_this_file_path, "../../common/config.yaml")

with open(_config_path, 'r', encoding="utf-8") as yamlfile:
    _cfg = yaml.safe_load(yamlfile)
config = DefaultMunch.fromDict(_cfg)
