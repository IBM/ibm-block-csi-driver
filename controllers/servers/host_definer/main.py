from controllers.servers.host_definer.host_definer_manager import HostDefinerManager


def main():
    host_definition_manager = HostDefinerManager()
    host_definition_manager.start_host_definition()


if __name__ == '__main__':
    main()
