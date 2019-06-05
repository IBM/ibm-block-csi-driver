#!/bin/bash

res=`docker rm $1`
res=`docker rm $2`
res=`docket network rm testnet`