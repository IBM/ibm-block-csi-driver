import os
from optparse import OptionParser
from controller.storage_agent.server.server import serve, generate_server_config


if __name__ == '__main__':
    parser = OptionParser()
    parser.add_option("-e", "--endpoint", dest="endpoint",
                      help="storage agent endpoint")

    options, _ = parser.parse_args()
    endpoint = options.endpoint

    if not endpoint:
        endpoint = os.environ.get("ENDPOINT")
    if not endpoint:
        raise Exception('neither "--endpoint" nor env ENDPOINT is set')
    max_workers = os.environ.get("WORKERS")
    serve(generate_server_config(endpoint, max_workers))
