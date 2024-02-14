package spec 


#BuildStep: close({
   command: string 
   env?: [string]: string
})

#CacheDirConfig: close({
    // TODO: specify further
    mode?: string
    key?: string
    include_distro_key?: bool
    include_arch_key?: bool
})

#Command: close({
   dir?: string
   mounts?: [#SourceMount]
   cache_dirs?: [string]: #CacheDirConfig
   env?: [string]: string
   steps?: [#BuildStep]
})

#SourceMount: close({
    dest: string
    // structural recursion must terminate
    spec: null | #Source
})

#SourceContext: close({
    name: string
})

#SourceImage: close({
    ref?: string
    cmd?: #Command
})

#SourceHTTP: close({
    // TODO: specify further
    url?: string
})

#SourceBuild: close({
    source?: #Source
    dockerfile?: string
    inline?: string
    target?: string
    args: [string]: string
})

#SourceGit: close({
    url?: string
    commit?: string
    keepGitDir?: bool
})

#Source: close({
    path?: string
    context?: #SourceContext
    git?: #SourceGit
    http?: #SourceHTTP
    build?: #SourceBuild
    image?: #SourceImage
})

#PackageDependencies: close({
    build?: [string]: ([] | [string])
    runtime?: [string]: ([] | [string])
    recommends?: [string]: ([] | [string])
    test?: [string]: ([] | [string])
})

#ArtifactBuild: close({
    steps: [#BuildStep]
    env?: [string]: string
})

#ArtifactConfig: close({
    name: string
    subPath?: string
})

#Artifacts: close({
    binaries?: [string]: #ArtifactConfig | null
    manpages?: [string]: #ArtifactConfig | null
})

#ImageConfig: close({
    entrypoint?: string
    cmd?: string
    env?: [string]
    labels?: [string]: string
    volumes?: [string]: null
    working_dir?: string
    stop_signal?: string
    base?: string
})

// TODO: export somehow?
#Spec: close({
    name: string
    description: string
    website: string
    version: string
    revision: uint
    vendor: string
    packager: string
    license: string

    sources: [string]: #Source

    dependencies: #PackageDependencies
    build: #ArtifactBuild
    artifacts: #Artifacts
    image: #ImageConfig
})

#Spec