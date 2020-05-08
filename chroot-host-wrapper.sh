#!/usr/bin/env bash

ME=`basename "$0"`

DIR="/host"   # The CSI node daemonset mount the / of the host into /host inside the container.
if [ ! -d "${DIR}" ]; then
    echo "Could not find docker engine host's filesystem at expected location: ${DIR}"
    exit 1
fi

exec chroot $DIR /usr/bin/env -i PATH="/sbin:/bin:/usr/bin" ${ME} "${@:1}"

