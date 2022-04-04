import os.path

import yaml
from munch import DefaultMunch


class Config:
    def __init__(self):
        my_path = os.path.abspath(os.path.dirname(__file__))
        path = os.path.join(my_path, "../../common/config.yaml")

        with open(path, 'r') as yamlfile:
            self.cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)

    @property
    def identity(self):
        return DefaultMunch.fromDict(self.cfg['identity'])

    @property
    def controller(self):
        return DefaultMunch.fromDict(self.cfg['controller'])

    @property
    def parameters(self):
        return DefaultMunch.fromDict(self.cfg['parameters'])


config = Config()
