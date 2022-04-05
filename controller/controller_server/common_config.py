import os.path

import yaml
from munch import DefaultMunch

my_path = os.path.abspath(os.path.dirname(__file__))
path = os.path.join(my_path, "../../common/config.yaml")

with open(path, 'r') as yamlfile:
    cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
config = DefaultMunch.fromDict(cfg)
