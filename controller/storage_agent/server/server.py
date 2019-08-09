from concurrent import futures
import os
import time
from collections import namedtuple
from ..logger.logger import get_logger
from .utils import get_array_connection_info_from_secret


import grpc
from grpc_reflection.v1alpha import reflection

from ..generated import storageagent_pb2
from ..generated import storageagent_pb2_grpc
from ...array_action.array_connection_manager import ArrayConnectionManager

_ONE_DAY_IN_SECONDS = 60 * 60 * 24
_DEFAULT_MAX_WORKERS = 10
logger = get_logger()


class StorageAgent(storageagent_pb2_grpc.StorageAgentServicer):

    def CreateHost(self, request, context):
        secrets = request.secrets
        user, password, array_addresses, array_type = get_array_connection_info_from_secret(secrets)

        with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:

            host = array_mediator.create_host(
                request.name,
                iscsi_ports=request.iqns, fc_ports=request.wwpns,
                host_type=request.type
            )
            return storageagent_pb2.CreateHostReply(
                host=storageagent_pb2.Host(
                    identifier=host.id,
                    name=host.name,
                    status=host.status,
                    array=",".join(array_addresses),
                )
            )

    def DeleteHost(self, request, context):
        secrets = request.secrets
        user, password, array_addresses, array_type = get_array_connection_info_from_secret(secrets)

        with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:
            array_mediator.delete_host(host_name=request.name, host_id=request.identifier)
            return storageagent_pb2.DeleteHostReply()

    def ListHosts(self, request, context):
        secrets = request.secrets
        user, password, array_addresses, array_type = get_array_connection_info_from_secret(secrets)

        with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:
            hosts = []
            for host in array_mediator.list_hosts(host_name=request.name, host_id=request.identifier):
                hosts.append(
                    storageagent_pb2.Host(
                        identifier=host.id,
                        name=host.name,
                        status=host.status,
                        array=",".join(array_addresses),
                    )
                )
            return storageagent_pb2.ListHostsReply(hosts=hosts)

    def ListIscsiTargets(self, request, context):
        secrets = request.secrets
        user, password, array_addresses, array_type = get_array_connection_info_from_secret(secrets)

        with ArrayConnectionManager(user, password, array_addresses, array_type) as array_mediator:
            targets = []
            for target in array_mediator.get_iscsi_targets():
                targets.append(
                    storageagent_pb2.IscsiTarget(
                        address=target.ip_address,
                        iqn=target.iqn,
                    )
                )
            return storageagent_pb2.ListIscsiTargetsReply(targets=targets)


ServerConfig = namedtuple('ServerConfig', ['address', 'max_workers'])


def generate_server_config(endpoint, max_workers=None):
    return ServerConfig(address=endpoint, max_workers=max_workers or _DEFAULT_MAX_WORKERS)


def serve(server_config):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=server_config.max_workers))
    storageagent_pb2_grpc.add_StorageAgentServicer_to_server(StorageAgent(), server)
    service_names = (
        storageagent_pb2.DESCRIPTOR.services_by_name['StorageAgent'].full_name,
        reflection.SERVICE_NAME,
    )
    reflection.enable_server_reflection(service_names, server)
    server.add_insecure_port(server_config.address)
    logger.info("Starting server on {}".format(server_config.address))
    server.start()
    logger.info("Server is started")
    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)
        logger.info("Server is stopped")
