TARGET                           DESCRIPTION
azlinux3/container (default)     Builds a container image for Azure Linux 3
azlinux3/container/depsonly      Builds a container image with only the runtime dependencies installed.
azlinux3/rpm                     Builds an rpm and src.rpm.
azlinux3/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
azlinux3/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
azlinux3/rpm/debug/spec          Outputs the generated RPM spec file
azlinux3/worker                  Builds the base worker image responsible for building the rpm
bionic/deb (default)             Builds a deb package.
bionic/dsc                       Builds a Debian source package.
bionic/testing/container         Builds a container image for testing purposes only.
bionic/worker                    Builds the worker image.
bookworm/deb (default)           Builds a deb package.
bookworm/dsc                     Builds a Debian source package.
bookworm/testing/container       Builds a container image for testing purposes only.
bookworm/worker                  Builds the worker image.
bullseye/deb (default)           Builds a deb package.
bullseye/dsc                     Builds a Debian source package.
bullseye/testing/container       Builds a container image for testing purposes only.
bullseye/worker                  Builds the worker image.
debug/gomods                     Outputs all the gomodule dependencies for the spec
debug/cargohome                  Outputs all the cargohome dependencies for the spec
debug/resolve                    Outputs the resolved dalec spec file with build args applied.
debug/sources                    Outputs all sources from a dalec spec file.
focal/deb (default)              Builds a deb package.
focal/dsc                        Builds a Debian source package.
focal/testing/container          Builds a container image for testing purposes only.
focal/worker                     Builds the worker image.
jammy/deb (default)              Builds a deb package.
jammy/dsc                        Builds a Debian source package.
jammy/testing/container          Builds a container image for testing purposes only.
jammy/worker                     Builds the worker image.
mariner2/container (default)     Builds a container image for CBL-Mariner 2
mariner2/container/depsonly      Builds a container image with only the runtime dependencies installed.
mariner2/rpm                     Builds an rpm and src.rpm.
mariner2/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
mariner2/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
mariner2/rpm/debug/spec          Outputs the generated RPM spec file
mariner2/worker                  Builds the base worker image responsible for building the rpm
noble/deb (default)              Builds a deb package.
noble/dsc                        Builds a Debian source package.
noble/testing/container          Builds a container image for testing purposes only.
noble/worker                     Builds the worker image.
windowscross/container (default) Builds binaries and installs them into a Windows base image
windowscross/worker              Builds the base worker image responsible for building the package
windowscross/zip                 Builds binaries combined into a zip file
