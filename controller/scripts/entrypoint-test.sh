#!/bin/bash -x
exec /bin/nosetests --with-coverage --cover-xml --cover-xml-file=/driver/coverage/.coverage $@
