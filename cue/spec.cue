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

// this alias exists for the sake of being explicit, and in case we want to add restrictions later
// on what kind of filepaths we allow in Dalec
let filepath = string

// {0, ..., 511} = {0o000, ..., 0o777} are valid unix perms
let perms = >= 0 & <= 0o777

// a source name must be alphanumeric, with the inclusion of '_' and '-'
// TODO: consider including '.' to allow sources with a file extension
let sourceName = =~ "^[a-zA-Z0-9_-.]+$"

#BuildStep: close({
   command: string 
   env?: [string]: string 
})

#CacheDirConfig: close({
    mode?: "shared" | "private" | "locked" 
    key?: string 
    include_distro_key?: bool 
    include_arch_key?: bool 
})

#Command: close({
   dir?: filepath 
   mounts?: [...#SourceMount] 
   cache_dirs?: [filepath]: #CacheDirConfig 
   env?: [string]: string 
   steps: [...#BuildStep] 
})

#SourceMount: {
    dest: filepath 
    // structural cycle formed by source.image.mounts.spec.source must terminate somehow for cue to accept
    // even though there are non-recursive sources, (i.e., it is not required that SourceMount contain a recursive source, so there is
    // implicitly a base case), cue's cycle detection is not currently sufficient to detect this.
    spec: null | #Source 
}

#SourceContext: close({
    name?: string
}) 

#SourceDockerImage: close({ 
    ref: string 
    cmd?: #Command 
})

#SourceHTTP: close({
    // TODO: specify url field further?
    url: string 
})

#SourceBuild: close({
    source?: #SubSource 
    dockerfile_path?: filepath 
    target?: string 
    args?: [string]: string 
})

#SourceGit: {
    // TODO: specify URL field further?
    url: string 
    commit?: string 
    keepGitDir?: bool 
}

#SourceInlineFile: close({
    contents?: string 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
})

#SourceInlineDir: close({
    files?: [sourceName]: #SourceInlineFile 
    permissions?: perms 
    uid?: >= 0 
    gid?: >= 0 
})

#SourceInline: { file: #SourceInlineFile } | 
                { dir: #SourceInlineDir } 

#SourceVariant: { context: #SourceContext } | 
                { git: #SourceGit } |
                { build: #SourceBuild } |
                { http: #SourceHTTP } |
                { image: #SourceDockerImage } |
                #SourceInline 

// these are sources which are suitable as a sub-source for
// SourceBuild's .source
#SubSourceVariant: { context: #SourceContext } | 
                   { git: #SourceGit } |
                   { http: #SourceHTTP } |
                   { image: #SourceDockerImage } |
                    #SourceInline 

// base properties which are source-independent
#SourceBase: { path?: filepath 
               includes?: [...string] 
               excludes?: [...string] 
            }

#Source: {#SourceBase, #SourceVariant}
#SubSource: {#SourceBase, #SubSourceVariant}

#PackageDependencies: close({
    build?: [string]: (null | [...string]) 
    runtime?: [string]: (null | [...string]) 
    recommends?: [string]: (null | [...string]) 
    test?: [string]: (null | [...string]) 
})

#ArtifactBuild: close({
    steps: [...#BuildStep]  
    env?: [string]: string 
})

#ArtifactConfig: close({
    sub_path?: filepath 
    name?: string 
})

#Artifacts: close({
    binaries?: [filepath]: #ArtifactConfig 
    manpages?: [filepath]: #ArtifactConfig 
})

#CheckOutput: close({
    equals?: string 
    contains?: [...string] 
    matches?: string 
    starts_with?: string 
    ends_with?: string 
    empty?: bool 
 })

#TestStep: close({
    command?: string 
    env?: [string]: string 
    stdout?: #CheckOutput 
    stderr?: #CheckOutput 
    stdin?: string 
})

#FileCheckOutput: {
    #CheckOutput 
    permissions?: perms 
    isDir?: bool 
    notExist?: bool 
}

#TestSpec: close({
    name: string 
    dir?: filepath 
    mounts?: [...#SourceMount] 
    cacheDirs?: [string]: #CacheDirConfig 
    env?: [string]: string 
    steps: [...#TestStep] 
    files?: [filepath]: #FileCheckOutput 
})

#SymlinkTarget: close({
    path: filepath 
})

#PostInstall: close({
    symlinks?: [filepath]: #SymlinkTarget 
})

#ImageConfig: close({
    entrypoint?: string 
    cmd?: string 
    env?: [...string] 
    labels?: [string]: string 
    volumes?: [string]: null 
    working_dir?: string 
    stop_signal?: string 
    base?: string 
    post?: #PostInstall
    user?: string 
})

#Frontend: close({
    image: string 
    cmdline?: string 
})

#Target: close({
    dependencies?: #PackageDependencies 
    image?: #ImageConfig // c
    frontend?: #Frontend // c
    tests?: [...#TestSpec] // c
})

#PatchSpec: close({
    source: sourceName 
    strip?: int 
})

// test: valid and invalid times
#ChangelogEntry: close({
    date: time.Time
    author: string
    changes: [...string]
})

#Spec: close({
    name: string 
    description: string 
    website?: string 
    version: string 
    revision: >= 0 
    noArch?: bool 

    conflicts?: [string]: [string] 
    replaces?: [string]: [string] 
    provides?: [...string] 

    sources?: [sourceName]: #Source 

    patches?: [sourceName]: [...#PatchSpec] 

    build?: #ArtifactBuild  

    // should probably validate magic variables here 
    // TARGET* vars should not have any default values applied
    args?: [string]: (string | null) 

    license: string 

    vendor: string 

    packager: string 

    artifacts?: #Artifacts  

    targets?: [string]: #Target 

    dependencies?: #PackageDependencies 

    image?: #ImageConfig 

    changelog?: [...#ChangelogEntry] 

    tests?: [...#TestSpec] 
})

#Spec