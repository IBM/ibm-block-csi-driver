#!/bin/bash
if [[ "$(uname -m)" == "s390x" ]]; then \
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
  fi
exec python3.12 /driver/controllers/servers/csi/main.py $@