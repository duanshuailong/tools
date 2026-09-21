dpkg-scanpackages -m . /dev/null > Packages
dpkg-scanpackages -m . /dev/null | gzip -9c > Packages.gz
