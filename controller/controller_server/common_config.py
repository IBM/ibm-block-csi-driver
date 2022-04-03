import os.path

import yaml


class CommonConfig:
    def __init__(self):
        my_path = os.path.abspath(os.path.dirname(__file__))
        path = os.path.join(my_path, "../../common/config.yaml")

        with open(path, 'r') as yamlfile:
            cfg = yaml.safe_load(yamlfile)  # TODO: add the following when possible : Loader=yaml.FullLoader)
        self.plugin_identity = cfg['identity']
        self.controller_config = cfg['controller']

    def get_identity_config(self, attribute_name):
        return self.plugin_identity[attribute_name]

    def get_controller_config(self, attribute_name):
        return self.controller_config[attribute_name]


common_config = CommonConfig()
