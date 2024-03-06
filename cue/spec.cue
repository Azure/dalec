package spec 

import (
    "time"
)

// further specify string fields that deal with urls?
// move under dalec/
// slot in and run against all existing test specs

//    Create test harness
//    Test harness should verify that cue spec catches errors for subfields
//    particularly for things people are likely to make errors with

// allow any filepath with no whitespace characters. can further restrict later
let filepath =  =~ "^[^\\0\\s]+$"

// {0, ..., 511} = {0o000, ..., 0o777} are valid unix perms
let perms = >= 0 & <= 0o777

// a source name must be alphanumeric, with the inclusion of '_' and '-'
// TODO: consider including '.' to allow sources with a file extension
// TEST: invalid source names
#sourceName: =~ "^[a-zA-Z0-9_-]+$"

#BuildStep: close({
   command: string //c
   env?: [string]: string //c
})

// TEST: invalid cache keys
#CacheDirConfig: close({
    mode?: "shared" | "private" | "locked" //c
    key?: string //c
    include_distro_key?: bool //c
    include_arch_key?: bool //c
})

#Command: close({
   dir?: filepath //c
   mounts?: [#SourceMount] //c
   cache_dirs?: [filepath]: #CacheDirConfig //c
   env?: [string]: string //c
   steps: [#BuildStep] //c
})

// TEST: structural cycle case
#SourceMount: close({
    dest: filepath //c
    // structural cycle formed by source.image.mounts.spec.source must terminate somehow for cue to accept
    // TODO: look into how this cycle could be terminated further up the chain, as this currently would allow
    // a source mount with no provided spec which is invalid (though a significant edge case)
    spec: null | #Source //c
})

#SourceContext: close({
    name?: string
}) //c

#SourceDockerImage: close({ //c
    ref: string //c
    cmd?: #Command //c
})

#SourceHTTP: close({
    // TODO: specify url field further?
    url: string //c
})

#SourceBuild: close({
    source?: #SubSource //c
    dockerfile_path?: string //c
    target?: string //c
    args?: [string]: string //c
})

#SourceGit: close({
    // TODO: specify URL field further?
    url: string //c
    commit?: string //c
    keepGitDir?: bool //c
})

// TEST UID/GID < 0
#SourceInlineFile: close({
    contents?: string //c
    permissions?: perms //c
    uid?: >= 0 //c
    gid?: >= 0 //c
})


// TEST UID/GID < 0
#SourceInlineDir: close({
    files?: [string]: #SourceInlineFile //c
    permissions?: perms //c
    uid?: >= 0 //c
    gid?: >= 0 //c
})

#SourceInline: { file: #SourceInlineFile } | 
                { dir: #SourceInlineDir } //c

#SourceVariant: { context: #SourceContext } | 
                { git: #SourceGit } |
                { build: #SourceBuild } |
                { http: #SourceHTTP } |
                { image: #SourceDockerImage } |
                #SourceInline //c

// these are sources which are suitable as a sub-source for
// SourceBuild's .source
#SubSourceVariant: { context: #SourceContext } | 
                   { git: #SourceGit } |
                   { http: #SourceHTTP } |
                   { image: #SourceDockerImage } |
                    #SourceInline //c

// base properties which are source-independent
#SourceBase: { path?: string //c
               includes?: [string] //c
               excludes?: [string] //c
            }

#Source: {#SourceBase, #SourceVariant}
#SubSource: {#SourceBase, #SubSourceVariant}

#PackageDependencies: close({
    build?: [string]: (null | [string]) //c
    runtime?: [string]: (null | [string]) //c
    recommends?: [string]: (null | [string]) //c
    test?: [string]: (null | [string]) //c
})

#ArtifactBuild: close({
    steps: [#BuildStep]  //c
    env?: [string]: string //c
})

#ArtifactConfig: close({
    // TODO: restrict path?
    subPath?: string //c
    name?: string //c
})

#Artifacts: close({
    binaries?: [string]: #ArtifactConfig //c
    manpages?: [string]: #ArtifactConfig //c
})

// TEST: mixes of fields set
#CheckOutput: close({
    equals?: string //c
    contains?: [string] //c
    matches?: string //c
    starts_with?: string //c
    ends_with?: string //c
    empty?: bool //c
 })

#TestStep: close({
    command?: string //c
    env?: [string]: string //c
    stdout?: #CheckOutput //c
    stderr?: #CheckOutput //c
    stdin?: string //c
})

#FileCheckOutput: {
    #CheckOutput //c
    permissions?: perms //c
    isDir?: bool //c
    notExist?: bool //c
}

// TEST: empty steps
#TestSpec: close({
    name: string //c
    dir?: filepath //c
    mounts?: [#SourceMount] //c
    cacheDirs?: [string]: #CacheDirConfig //c
    env?: [string]: string //c
    steps: [#TestStep] //c
    files?: [filepath]: #FileCheckOutput //c
})

// TEST: invalid path
#SymlinkTarget: close({
    path: string //c
})

#PostInstall: close({
    symlinks?: [string]: #SymlinkTarget //c
})

#ImageConfig: close({
    entrypoint?: string //c
    cmd?: string //c
    env?: [string] //c
    labels?: [string]: string //c
    volumes?: [string]: null //c
    working_dir?: string //c
    stop_signal?: string //c
    base?: string //c
    post?: #PostInstall
    user?: string //c
})

#Frontend: close({
    image: string //c
    cmdline?: string //c
})

#Target: close({
    dependencies?: #PackageDependencies //c
    image?: #ImageConfig // c
    frontend?: #Frontend // c
    tests?: [#TestSpec] // c
})


#PatchSpec: close({
    source: #sourceName //c
    strip?: int //c
})

// test: valid and invalid times
#ChangelogEntry: close({
    date: time.Time
    author: string
    changes: [string]
})

// test: multiple/no sources specified
#Spec: close({
    name: string //c
    description: string //c
    website?: string //c
    version: string //c
    revision: uint //c
    noArch?: bool //c

    conflicts?: [string]: [string] //c
    replaces?: [string]: [string] //c
    provides?: [string] //c

    sources?: [#sourceName]: #Source //c

    patches?: [#sourceName]: [#PatchSpec] //c

    build?: #ArtifactBuild //c 

    // should probably validate magic variables here 
    // TARGET* vars should not have any default values applied
    args?: [string]: (string | null) //c

    license: string //c

    vendor: string //c

    packager: string //c

    artifacts?: #Artifacts  //c

    targets?: [string]: #Target //c

    dependencies?: #PackageDependencies //c

    image?: #ImageConfig //c

    changelog?: [#ChangelogEntry] //c

    tests?: [#TestSpec] //c
})

#Spec