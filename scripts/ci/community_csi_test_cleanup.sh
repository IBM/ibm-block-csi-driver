#!/bin/bash -xe

if [ `docker ps -a | grep $1 | wc -l` != 0 ] ; then
    docker rm -f $1
fi