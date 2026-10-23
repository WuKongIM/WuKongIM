# Three-node first-grant sampler failure matrix

- Frozenhelperhardcodeslegacy node2 inmetadata/requestJSON/filenames; --node URL acceptsloopback but hasnonode-id flag. Root choseB explicitnode-id produceradapter, never sidecaroverridesof falseproducerlabels. Oldsource/artifacts remainimmutable.
- Newsource/binaryhead isnotready: keepbindingspending, no placeholderSOURCE/build/launcher, noexecution. Runtime mustmatchfullsourceHEAD/binary/helperSHA andrunhash/assignment/gen.
- Stale/oversized/aliased run-start receipt, wrongidentity/span259200/gen, lag>2s, clockconflict, missingphase: originalskip/partial rules, no fallbacktrigger/retry.
- One receipt canlaunchatmostthreehelpers, oneexplicitAPIURLperphysicalnode; singleclaim, distinctprivateoutput+logdirs, duplicateURLs/unsafe paths reject.
- Terminal/stop/signal betweenlaunches mayleavepartialcoverage; registereachhelper beforeanycancel. Unlaunchednodesremainexplicitlyskipped; no replacementrequest.
- Cancelallactivehelpers concurrently, keeporiginal2sTERM+1sKILL/join envelope aggregate. Preservehelper's registration/timer/curl joins; restrictescalationtoownedroots+descendants inexistingPGID. Original570sTERM+5sKILL remainsfinalcleanup.
- CurlHTTPerror/timeout/oversize/missingGOR remainsnode-specificpartial; noothernode maycoverit ortriggerretry. ExpectedthreeCPU8MiB plus3GOR4MiB caps=36MiB bodytotal.
- Helperlaunch times areobservedlocally; CPUsstarttimes/GOR5s offsets arenotexactlysynchronized. PhasefiledoesnotproveactiveSENDthroughout; profilesmaymixactive/drain.
- Additionaldiagnosticrequests increasefrom2to6 across3processes. Samplingdisturbance islarger andnotqualified; nextmeasuredwindow isnot acausalperformance comparisonorLinuxacceptance.
- No heap/context/moreendpoint/capchange/timeoutchange/candidateconfigchange; source/binarypending untilfutureapprovedexactbuild.

## Root choiceB before implementation

Unchangedproducer literalnode2 would falselylabelnode1/3. Root chose minimalexplicit --node-id adapter, default2; no sidecar overridesof falseproducerlabels. Validate1/2/3 before artifactI/O; deriveeverymetadata/requestnodefield andfilename fromNODE_ID. Preservealltimers/curlcaps/registration/auth/cancel/join/profileformats.

Tests-first cases: explicitnodes1/2/3 routecorrectlywithmatchingproducerIDs/files; absentflag preservesnode2; invalid/missing IDs failbeforeoutmkdir/curl; node3HTTPerrorremainsnode3partial; actualhelper signalcancel joinsownedfakecurl children and preservesnode3partial metadata, no secretinargv/output/env. NoTCP/profilebinaryfixtures.
