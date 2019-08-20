FROM registry.access.redhat.com/ubi7/ubi:7.6-177

ENV PATH="/opt/rh/rh-python36/root/usr/bin:${PATH}"﻿ \
    PYTHONPATH=/storage-agent \
    USER_UID=1001 \
    USER_NAME=storage-agent
RUN yum install -y rh-python36-python-pip.noarch && yum clean all

WORKDIR /storage-agent
COPY array_action controller/array_action
COPY common controller/common
COPY storage_agent controller/storage_agent
RUN pip3 install -r controller/storage_agent/requirements.txt

COPY storage_agent/build/docker-entrypoint.sh .
RUN chmod 755 docker-entrypoint.sh
USER ${USER_UID}

ENTRYPOINT ["./docker-entrypoint.sh"]