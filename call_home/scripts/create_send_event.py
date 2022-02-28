import json
import os
import time
import requests
from kubernetes import client, config

PRODUCT_ID = "5027566ef6c54de49028be7df25119e1"
PRODUCT_NAME = "IBM_System_Storage_Block_CSI_Driver"

config.load_incluster_config()
v1 = client.CoreV1Api()

stream = open('event_consts.json', 'r')
event_data = json.loads(stream.read())

kube_system_namespace = v1.read_namespace("kube-system")
kube_system_namespace_uid = kube_system_namespace.metadata.uid

event_time = time.strftime("%Y-%m-%d %I:%M:%S", time.gmtime())
local_event_time = time.strftime("%a %b %d %Y %I:%M:%S %Z", time.gmtime())
event_time_ms = int(time.time())

Instance_name = f"{PRODUCT_NAME}_Instance_cluster"
asset_id = f"{PRODUCT_ID}:kube_{kube_system_namespace_uid}"
virtual_id = f"{Instance_name}:{asset_id}"
event_id = f"{PRODUCT_NAME}-STOR-CSI-{asset_id}-primary-event"

event_data["event_time"] = event_time
event_data["local_event_time"] = local_event_time
event_data["event_time_ms"] = event_time_ms

event_data["agent"] = event_data["software_level"]["name"] = PRODUCT_NAME

event_data["asset_id"] = asset_id
event_data["asset_virtual_id"] = virtual_id
event_data["event_id"] = event_id
event_data["analytics_virtual_id"] = virtual_id
event_data["analytics_instance"] = asset_id

event_data["target_space"] = "test"

event = event_data["events"][0]

header = event["header"]
header["event_time"] = event_time
header["local_event_time"] = local_event_time
header["event_time_ms"] = event_time_ms
header["event_id"] = f"last_contact_asset_event_{event_id}_1"

payload = dict()
csi_version = os.environ.get("CSI_VERSION", "")
payload["csi_version"] = csi_version
payload["cluster_id"] = kube_system_namespace_uid

body = event["body"]
body["context"]["timestamp"] = event_time_ms
body["payload"] = payload

event["header"].update(header)
event["body"].update(body)

client_session = requests.Session()

url = "https://stg-edge-cdt.eventsgslb.ibm.com/connect/api/v1"
request = requests.Request("POST", url, data=json.dumps(event_data), headers={'content-type': 'application/json'})
response = client_session.send(client_session.prepare_request(request))
print(response)
