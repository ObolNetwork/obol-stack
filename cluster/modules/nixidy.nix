{
  nixidy = {
    target = {
      repository = "https://github.com/ObolNetwork/obol-stack";
      branch = "main";
      rootPath = ".manifests";
    };
    # build.revision =
    #   if (self ? rev)
    #   then self.rev
    #   else self.dirtyRev;
  };
}
