#!/usr/bin/python3.6

import os
from pyxcli.client import XCLIClient

user = os.environ["USERNAME"]
password = os.environ["PASSWORD"]
endpoint = os.environ["STORAGE_ARRAYS"]
pool = os.environ["POOL_NAME"]

client = XCLIClient.connect_multiendpoint_ssl(
                user,
                password,
                endpoint
            )

vol_list = client.cmd.vol_list(pool=pool).as_list

for vol in vol_list:
    print("deleting volume : {}".format(vol))
    client.cmd.vol_delete(vol=vol.name)
