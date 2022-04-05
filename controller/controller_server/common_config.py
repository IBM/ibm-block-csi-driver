import os.path

import yaml
from munch import DefaultMunch


class Config:
    def __init__(self):
        my_path = os.path.abspath(os.path.dirname(__file__))
        path = os.path.join(my_path, "../../common/config.yaml")

        with open(path, 'r') as yamlfile:
            cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
        self.config = DefaultMunch.fromDict(cfg)

    @property
    def identity(self):
        return self.config.identity

    @property
    def controller(self):
        return self.config.controller

    @property
    def parameters(self):
        return self.config.parameters


config = Config()
