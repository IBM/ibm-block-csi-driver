#!/bin/bash -xe

if [ `docker ps | grep $1 | wc -l` != 0 ] ; then
    docker stop $1
fi