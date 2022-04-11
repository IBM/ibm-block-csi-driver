import os.path

import yaml
from munch import DefaultMunch

_my_path = os.path.abspath(os.path.dirname(__file__))
_path = os.path.join(_my_path, "../../common/config.yaml")

with open(_path, 'r') as yamlfile:
    _cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
config = DefaultMunch.fromDict(_cfg)
