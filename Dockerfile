FROM centos:7
RUN yum --enablerepo=extras -y install epel-release && yum -y install python2-pip 
RUN pip install "grpcio==1.20.1" "grpcio-tools==1.20.1" "protobuf==3.7.1" "futures==3.2.0" "pyyaml==5.1" &&\
    # A9000 python client
    pip install "pyxcli==1.1.7" 

COPY . /driver
WORKDIR /driver
ENV PYTHONPATH=/driver
ENTRYPOINT ["python", "/driver/controller/controller_server/csi_controller_server.py"]

