"use strict";(self.webpackChunkwebsite=self.webpackChunkwebsite||[]).push([[18],{5364:(e,n,s)=>{s.r(n),s.d(n,{assets:()=>r,contentTitle:()=>a,default:()=>h,frontMatter:()=>c,metadata:()=>o,toc:()=>d});var i=s(4848),t=s(8453);const c={title:"Dalec Specification"},a=void 0,o={id:"spec",title:"Dalec Specification",description:"Dalec YAML specification is a declarative format for building system packages and containers from those packages.",source:"@site/docs/spec.md",sourceDirName:".",slug:"/spec",permalink:"/dalec/spec",draft:!1,unlisted:!1,editUrl:"https://github.com/Azure/dalec/blob/main/website/docs/spec.md",tags:[],version:"current",frontMatter:{title:"Dalec Specification"},sidebar:"sidebar",previous:{title:"Virtual Packages",permalink:"/dalec/virtual-packages"},next:{title:"Sources",permalink:"/dalec/sources"}},r={},d=[{value:"Args section",id:"args-section",level:2},{value:"Metadata section",id:"metadata-section",level:2},{value:"Targets section",id:"targets-section",level:2},{value:"Image section",id:"image-section",level:3},{value:"Package Config section",id:"package-config-section",level:3},{value:"Sources section",id:"sources-section",level:2},{value:"Dependencies section",id:"dependencies-section",level:2},{value:"Build section",id:"build-section",level:2},{value:"Artifacts section",id:"artifacts-section",level:2},{value:"Tests section",id:"tests-section",level:2}];function l(e){const n={a:"a",admonition:"admonition",code:"code",em:"em",h2:"h2",h3:"h3",li:"li",p:"p",pre:"pre",strong:"strong",ul:"ul",...(0,t.R)(),...e.components};return(0,i.jsxs)(i.Fragment,{children:[(0,i.jsx)(n.p,{children:"Dalec YAML specification is a declarative format for building system packages and containers from those packages."}),"\n",(0,i.jsx)(n.p,{children:"This section provides a high level overview of the Dalec YAML specification. For more detailed information, please see the applicable sections."}),"\n",(0,i.jsx)(n.admonition,{type:"note",children:(0,i.jsxs)(n.p,{children:["All Dalec spec YAMLs must start with ",(0,i.jsx)(n.code,{children:"# syntax=ghcr.io/azure/dalec/frontend:latest"}),"."]})}),"\n",(0,i.jsx)(n.p,{children:"Dalec spec YAMLs are composed of the following sections:"}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#args-section",children:"Args section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#metadata-section",children:"Metadata section"})}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.a,{href:"#targets-section",children:"Targets section"}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#image-section",children:"Image section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#package-config-section",children:"Package Config section"})}),"\n"]}),"\n"]}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#sources-section",children:"Sources section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#dependencies-section",children:"Dependencies section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#build-section",children:"Build section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#artifacts-section",children:"Artifacts section"})}),"\n",(0,i.jsx)(n.li,{children:(0,i.jsx)(n.a,{href:"#tests-section",children:"Tests section"})}),"\n"]}),"\n",(0,i.jsx)(n.h2,{id:"args-section",children:"Args section"}),"\n",(0,i.jsx)(n.p,{children:"Args section is an optional section that is used to define the arguments that can be passed to the spec. These arguments can be used in the spec to define the version, commit, or any other fields."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"args:\n  VERSION: 1.0.0\n  COMMIT: 55019c83b0fd51ef4ced8c29eec2c4847f896e74\n  REVISION: 1\n"})}),"\n",(0,i.jsx)(n.p,{children:"There are a few built-in arguments which, if present, Dalec will substitute values for. They are listed as examples in the following block:"}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"args:\n  TARGETOS:\n  TARGETARCH:\n  TARGETPLATFORM:\n  TARGETVARIANT:\n"})}),"\n",(0,i.jsxs)(n.p,{children:["These arguments are set based on the default docker platform for the machine, ",(0,i.jsx)(n.em,{children:"unless"})," the platform is overriden explicitly in the docker build with ",(0,i.jsx)(n.code,{children:"--platform"}),". For example, upon invoking ",(0,i.jsx)(n.code,{children:"docker build"})," on a Linux amd64 machine, we would have ",(0,i.jsx)(n.code,{children:"TARGETOS=linux"}),", ",(0,i.jsx)(n.code,{children:"TARGETARCH=amd64"}),", ",(0,i.jsx)(n.code,{children:"TARGETPLATFORM=linux/amd64"}),"."]}),"\n",(0,i.jsx)(n.admonition,{type:"note",children:(0,i.jsxs)(n.p,{children:["No default value should be included for these build args. These args are opt-in. If you haven't listed them in the args section as shown above, Dalec will ",(0,i.jsx)(n.strong,{children:"not"})," substitute values for them."]})}),"\n",(0,i.jsx)(n.h2,{id:"metadata-section",children:"Metadata section"}),"\n",(0,i.jsx)(n.p,{children:"Metadata section is used to define the metadata of the spec. This metadata includes the name, packager, vendor, license, website, and description of the spec."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"name: My-Package\nlicense: Apache-2.0\ndescription: This is a sample package\nversion: ${VERSION}\nrevision: ${REVISION}\npackager: Dalec Authors\nvendor: Dalec Authors\nwebsite: https://github.com/foo/bar\n"})}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"name"}),": The name of the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"license"}),": The license of the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"description"}),": The description of the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"version"}),": The version of the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"revision"}),": The revision of the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"packager"}),": The packager of the package. This is an optional field."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"vendor"}),": The vendor of the package. This is an optional field."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"website"}),": The website of the package. This is an optional field."]}),"\n"]}),"\n",(0,i.jsx)(n.admonition,{type:"tip",children:(0,i.jsxs)(n.p,{children:["Any field at the top-level that begins with ",(0,i.jsx)(n.code,{children:"x-"})," will be ignored by Dalec. This allows for custom fields to be added to the spec. For example, ",(0,i.jsx)(n.code,{children:"x-foo: bar"}),". Any other field that is not recognized by Dalec will result in a validation error."]})}),"\n",(0,i.jsx)(n.h2,{id:"targets-section",children:"Targets section"}),"\n",(0,i.jsx)(n.p,{children:"Targets section is used to define configuration per target. Each target can have its own configuration."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"targets:\n  mariner2:\n    image:\n      base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0\n      post:\n        symlinks:\n          /usr/bin/my-binary:\n            path: /my-binary\n    package_config:\n      signer:\n        image: azcutools.azurecr.io/azcu-dalec/signer:latest\n  azlinux3:\n    # same fields as above\n  windowscross:\n    # same fields as above\n"})}),"\n",(0,i.jsxs)(n.p,{children:["Valid targets are ",(0,i.jsx)(n.code,{children:"mariner2"}),", ",(0,i.jsx)(n.code,{children:"azlinux3"}),", ",(0,i.jsx)(n.code,{children:"windowscross"}),"."]}),"\n",(0,i.jsxs)(n.p,{children:["For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/targets",children:"Targets"}),"."]}),"\n",(0,i.jsx)(n.h3,{id:"image-section",children:"Image section"}),"\n",(0,i.jsx)(n.p,{children:"Image section is used to define the base image and post processing for the image."}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"base"}),": The base image for the target."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"post"}),": The post processing for the image, such as symlinks."]}),"\n"]}),"\n",(0,i.jsx)(n.h3,{id:"package-config-section",children:"Package Config section"}),"\n",(0,i.jsx)(n.p,{children:"Package Config section is used to define the package configuration for the target."}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"signer"}),": The signer configuration for the package. This is used to sign the package. For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/signing",children:"Signing Packages"}),"."]}),"\n"]}),"\n",(0,i.jsx)(n.h2,{id:"sources-section",children:"Sources section"}),"\n",(0,i.jsx)(n.p,{children:"Sources section is used to define the sources for the spec. These sources can be used to define the source code, patches, or any other files needed for the spec."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:'sources:\n  foo:\n    git:\n      url: https://github.com/foo/bar.git\n      commit: ${COMMIT}\n      keepGitDir: true\n    generate:\n    - gomod: {}\n  foo-patch:\n    http:\n      url: https://example.com/foo.patch\n  foo-inline:\n    inline:\n      - name: my-script\n        content: |\n          #!/bin/sh\n          echo "Hello, World!"\n  foo-context:\n    context: {}\n'})}),"\n",(0,i.jsxs)(n.p,{children:["For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/sources",children:"Sources"}),"."]}),"\n",(0,i.jsx)(n.h2,{id:"dependencies-section",children:"Dependencies section"}),"\n",(0,i.jsx)(n.p,{children:"The dependencies section is used to define the dependencies for the spec. These dependencies can be used to define the build dependencies, runtime dependencies, or any other dependencies needed for the package."}),"\n",(0,i.jsx)(n.admonition,{type:"tip",children:(0,i.jsxs)(n.p,{children:["Dependencies can be defined at the root level or under a target. If defined under a target, the dependencies will only be used for that target. Dependencies under a target will override dependencies at the root level. For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/targets",children:"Targets"}),"."]})}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"dependencies:\n  build:\n    - golang\n    - gcc\n  runtime:\n    - libfoo\n    - libbar\n"})}),"\n",(0,i.jsxs)(n.p,{children:["Sometimes you may need to add extra repositories in order to fulfill the\nspecified dependencies.\nYou can do this by adding these to the ",(0,i.jsx)(n.code,{children:"extra_repos"})," field.\nThe ",(0,i.jsx)(n.code,{children:"extra_repos"})," field takes a list of repository configurations with optional\npublic key data and optional repo data (e.g. the actual data of a repository).\nSee ",(0,i.jsx)(n.a,{href:"/dalec/repositories",children:"repositories"})," for more details on repository configs"]}),"\n",(0,i.jsx)(n.h2,{id:"build-section",children:"Build section"}),"\n",(0,i.jsx)(n.p,{children:"Build section is used to define the build steps for the spec. These build steps can be used to define the build commands, environment variables, or any other build configuration needed for the package."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:'build:\n  env:\n    TAG: v${VERSION}\n    GOPROXY: direct\n    CGO_ENABLED: "0"\n    GOOS: ${TARGETOS}\n  steps:\n    - command: |\n        go build -ldflags "-s -w -X github.com/foo/bar/version.Version=${TAG}" -o /out/my-binary ./cmd/my-binary\n'})}),"\n",(0,i.jsxs)(n.ul,{children:["\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"env"}),": The environment variables for the build."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"steps"}),": The build steps for the package."]}),"\n",(0,i.jsxs)(n.li,{children:[(0,i.jsx)(n.code,{children:"network_mode"}),": Set the network mode to use for build steps (accepts: empty, ",(0,i.jsx)(n.code,{children:"none"}),", ",(0,i.jsx)(n.code,{children:"sandbox"}),")"]}),"\n"]}),"\n",(0,i.jsx)(n.admonition,{type:"tip",children:(0,i.jsxs)(n.p,{children:["TARGETOS is a built-in argument that Dalec will substitute with the target OS value. For more information, please see ",(0,i.jsx)(n.a,{href:"#args-section",children:"Args section"}),"."]})}),"\n",(0,i.jsx)(n.admonition,{type:"tip",children:(0,i.jsxs)(n.p,{children:["Set ",(0,i.jsx)(n.code,{children:"network_mode"})," to ",(0,i.jsx)(n.code,{children:"sandbox"})," to allow internet access during build"]})}),"\n",(0,i.jsx)(n.h2,{id:"artifacts-section",children:"Artifacts section"}),"\n",(0,i.jsx)(n.p,{children:"Artifacts section is used to define the artifacts for the spec. These artifacts can be used to define the output of the build, such as the package or container image."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:"artifacts:\n  binaries:\n     foo/my-binary: {}\n  manpages:\n    src/man/man8/*:\n      subpath: man8\n"})}),"\n",(0,i.jsxs)(n.p,{children:["For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/artifacts",children:"Artifacts"})]}),"\n",(0,i.jsx)(n.h2,{id:"tests-section",children:"Tests section"}),"\n",(0,i.jsx)(n.p,{children:"Tests section is used to define the tests for the spec. These tests can be used to define the test cases, steps, or any other tests needed for the package."}),"\n",(0,i.jsx)(n.pre,{children:(0,i.jsx)(n.code,{className:"language-yaml",children:'tests:\n  - name: check permissions\n    files:\n      /usr/bin/my-binary:\n        permissions: 0755\n  - name: version reporting\n    steps:\n      - command: my-binary --version\n        stdout:\n          starts_with: "my-binary version ${VERSION}"\n          contains:\n            - "libseccomp: "\n        stderr:\n          empty: true\n'})}),"\n",(0,i.jsxs)(n.p,{children:["For more information, please see ",(0,i.jsx)(n.a,{href:"/dalec/testing",children:"Testing"}),"."]})]})}function h(e={}){const{wrapper:n}={...(0,t.R)(),...e.components};return n?(0,i.jsx)(n,{...e,children:(0,i.jsx)(l,{...e})}):l(e)}},8453:(e,n,s)=>{s.d(n,{R:()=>a,x:()=>o});var i=s(6540);const t={},c=i.createContext(t);function a(e){const n=i.useContext(c);return i.useMemo((function(){return"function"==typeof e?e(n):{...n,...e}}),[n,e])}function o(e){let n;return n=e.disableParentContext?"function"==typeof e.components?e.components(t):e.components||t:a(e.components),i.createElement(c.Provider,{value:n},e.children)}}}]);