#!/bin/bash
if [ "$TARGETARCH" = "s390x" ]; then \
  export PROTOCOL_BUFFERS_PYTHON_IMPLEMENTATION=python
  fi
exec python3.9 /driver/controllers/servers/csi/main.py $@
