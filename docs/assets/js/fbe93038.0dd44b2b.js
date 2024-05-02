"use strict";(self.webpackChunkwebsite=self.webpackChunkwebsite||[]).push([[133],{3510:(e,n,t)=>{t.r(n),t.d(n,{assets:()=>l,contentTitle:()=>i,default:()=>d,frontMatter:()=>a,metadata:()=>o,toc:()=>h});var s=t(4848),c=t(8453);const a={},i="Testing",o={id:"testing",title:"Testing",description:"Dalec supports adding tests to your spec file.",source:"@site/docs/testing.md",sourceDirName:".",slug:"/testing",permalink:"/dalec/testing",draft:!1,unlisted:!1,editUrl:"https://github.com/Azure/dalec/blob/main/website/docs/docs/testing.md",tags:[],version:"current",frontMatter:{},sidebar:"sidebar",previous:{title:"Sources",permalink:"/dalec/sources"},next:{title:"Signing Packages",permalink:"/dalec/signing"}},l={},h=[{value:"Checks for files",id:"checks-for-files",level:2},{value:"Check file existence",id:"check-file-existence",level:3},{value:"Check file contents",id:"check-file-contents",level:3},{value:"Check that a file contains some value:",id:"check-that-a-file-contains-some-value",level:4},{value:"Check that a file starts with some value:",id:"check-that-a-file-starts-with-some-value",level:4},{value:"Check that a file ends with some value:",id:"check-that-a-file-ends-with-some-value",level:4},{value:"Check that a file matches a regular expression:",id:"check-that-a-file-matches-a-regular-expression",level:4},{value:"Check that a file does not exist",id:"check-that-a-file-does-not-exist",level:4},{value:"Check that a path is a directory",id:"check-that-a-path-is-a-directory",level:4},{value:"Check file permissions",id:"check-file-permissions",level:4},{value:"Add multiple checks together",id:"add-multiple-checks-together",level:4},{value:"Run a command",id:"run-a-command",level:3},{value:"Capture stdio streams",id:"capture-stdio-streams",level:4},{value:"Inject a file into the container for additional checks",id:"inject-a-file-into-the-container-for-additional-checks",level:4}];function r(e){const n={code:"code",h1:"h1",h2:"h2",h3:"h3",h4:"h4",p:"p",pre:"pre",...(0,c.R)(),...e.components};return(0,s.jsxs)(s.Fragment,{children:[(0,s.jsx)(n.h1,{id:"testing",children:"Testing"}),"\n",(0,s.jsx)(n.p,{children:"Dalec supports adding tests to your spec file.\nThese tests are run against the container produced by your spec.\nDalec provides a few test helpers to make it easier to write tests when your image doesn't have the tools you need."}),"\n",(0,s.jsx)(n.h2,{id:"checks-for-files",children:"Checks for files"}),"\n",(0,s.jsx)(n.h3,{id:"check-file-existence",children:"Check file existence"}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /usr/bin/foo:\n"})}),"\n",(0,s.jsx)(n.h3,{id:"check-file-contents",children:"Check file contents"}),"\n",(0,s.jsx)(n.p,{children:"Here are some examples on how to check the contents and metadata of a file in the output container.\nYou can use these to check that the files you expect are present and have the correct contents."}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-file-contains-some-value",children:"Check that a file contains some value:"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the content of the file ",(0,s.jsx)(n.code,{children:"/etc/foo.conf"})," contains the string ",(0,s.jsx)(n.code,{children:"foo=bar"}),".\nYou can specify multiple values to check for."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                contains:\n                    - foo=bar\n"})}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-file-starts-with-some-value",children:"Check that a file starts with some value:"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the content of the file ",(0,s.jsx)(n.code,{children:"/etc/foo.conf"})," starts with the string ",(0,s.jsx)(n.code,{children:"foo"}),"."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                starts_with: foo\n"})}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-file-ends-with-some-value",children:"Check that a file ends with some value:"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the content of the file ",(0,s.jsx)(n.code,{children:"/etc/foo.conf"})," ends with the string ",(0,s.jsx)(n.code,{children:"bar"}),"."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                ends_with: bar\n"})}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-file-matches-a-regular-expression",children:"Check that a file matches a regular expression:"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the content of the file ",(0,s.jsx)(n.code,{children:"/etc/foo.conf"})," matches the regular expression ",(0,s.jsx)(n.code,{children:"foo=.*"}),"."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:'name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                matches: "foo=.*"\n'})}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-file-does-not-exist",children:"Check that a file does not exist"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the file ",(0,s.jsx)(n.code,{children:"/some/nonexistent/path"})," does not exist."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /some/nonexistent/path:\n                not_exist: true\n"})}),"\n",(0,s.jsx)(n.h4,{id:"check-that-a-path-is-a-directory",children:"Check that a path is a directory"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the path ",(0,s.jsx)(n.code,{children:"/some/path"})," is a directory."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /some/path:\n                is_dir: true\n"})}),"\n",(0,s.jsx)(n.h4,{id:"check-file-permissions",children:"Check file permissions"}),"\n",(0,s.jsxs)(n.p,{children:["Here we check that the file ",(0,s.jsx)(n.code,{children:"/etc/foo.conf"})," has the permissions ",(0,s.jsx)(n.code,{children:"0644"}),"."]}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                permissions: 0644\n"})}),"\n",(0,s.jsx)(n.h4,{id:"add-multiple-checks-together",children:"Add multiple checks together"}),"\n",(0,s.jsx)(n.p,{children:"You can add multiple checks together to check multiple things about a file.\nNote that some checks are mutually exclusive and will cause an error if you try to use them together."}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:'name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        files:\n            /etc/foo.conf:\n                contains:\n                    - foo=bar\n                starts_with: foo\n                ends_with: bar\n                matches: "foo=.*"\n                permissions: 0644\n'})}),"\n",(0,s.jsx)(n.h3,{id:"run-a-command",children:"Run a command"}),"\n",(0,s.jsx)(n.p,{children:"You can run a command in the container and check the stdout and/or stderr of that command.\nThese commands are run before any of the file checks are run and may influence the output of those checks."}),"\n",(0,s.jsx)(n.p,{children:"Because these images often will not have a shell in them, if you want a shell you'll need to run it explicitly."}),"\n",(0,s.jsx)(n.h4,{id:"capture-stdio-streams",children:"Capture stdio streams"}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:'name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        steps:\n            -\n                command: echo "hello world"\n                stdout:\n                    equals:\n                        - hello world\\n\n            -\n                # Note: the image used for this test would need to have a shell in it for this to work\n                command: /bin/sh -c \'echo "hello world" >&2\'\n                stderr:\n                    equals:\n                        - hello world\\n\n'})}),"\n",(0,s.jsx)(n.p,{children:"Pass in stdin to the command:"}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        steps:\n            -\n                command: cat\n                stdin: hello world\n                stdout:\n                    equals:\n                        - hello world\n"})}),"\n",(0,s.jsx)(n.h4,{id:"inject-a-file-into-the-container-for-additional-checks",children:"Inject a file into the container for additional checks"}),"\n",(0,s.jsx)(n.p,{children:"Test cases support source mounts just like in the main spec.\nYou can use this to inject files, build helper binaries, or add whatever you need to run a test."}),"\n",(0,s.jsx)(n.pre,{children:(0,s.jsx)(n.code,{className:"language-yaml",children:"name: My Package\n# ... other spec fields\n\ntests:\n    -\n        name: My Test case\n        mounts:\n            -\n                dest: /target/mount/path\n                spec:\n                    build:\n                        source:\n                            inline:\n                                file:\n                                    contents: |\n                                        FROM busybox\n                                        RUN echo hello > /hello\n\n                                        FROM scratch\n                                        COPY --from=busybox /hello /hello\n        steps:\n            -\n                command: cat /path/in/container\n                stdout:\n                    equals: hello\n\n"})})]})}function d(e={}){const{wrapper:n}={...(0,c.R)(),...e.components};return n?(0,s.jsx)(n,{...e,children:(0,s.jsx)(r,{...e})}):r(e)}},8453:(e,n,t)=>{t.d(n,{R:()=>i,x:()=>o});var s=t(6540);const c={},a=s.createContext(c);function i(e){const n=s.useContext(a);return s.useMemo((function(){return"function"==typeof e?e(n):{...n,...e}}),[n,e])}function o(e){let n;return n=e.disableParentContext?"function"==typeof e.components?e.components(c):e.components||c:i(e.components),s.createElement(a.Provider,{value:n},e.children)}}}]);