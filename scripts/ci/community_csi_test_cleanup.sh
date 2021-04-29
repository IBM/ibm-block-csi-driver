#!/bin/bash -xe

if [ `docker ps -a | grep $1 | wc -l` != 0 ] ; then
    docker stop $1
    docker rm $1
fi
