# Changelog

## [1.9.3](https://github.com/mynaparrot/plugNmeet-server/compare/v1.9.2...v1.9.3) (2025-09-20)


### Bug Fixes

* **analytics:** Make event handling stateless and remove lock ([699c496](https://github.com/mynaparrot/plugNmeet-server/commit/699c4963ab375597926beb6609b55eca83576e3e))
* **analytics:** Optimize export process with Redis SCAN ([942d976](https://github.com/mynaparrot/plugNmeet-server/commit/942d976e3a2aa881292ffd846500917edd625f92))
* **analytics:** Prevent race condition in analytics export ([0107211](https://github.com/mynaparrot/plugNmeet-server/commit/010721160ed17ed2d1b5c213311b7398307eacab))
* **bug:** accidentally deleted important method to handle `HandleGetClientFiles` ([d251223](https://github.com/mynaparrot/plugNmeet-server/commit/d251223cd323325d5e46fc98e64627cd03cb2560))
* **cleanup:** added more logs ([1b4da4b](https://github.com/mynaparrot/plugNmeet-server/commit/1b4da4b786fabead9660018621afc67543ac6432))
* **db:** added option to configure database read replicas ([f5197ee](https://github.com/mynaparrot/plugNmeet-server/commit/f5197ee6699d55c70b05e1e7ebb7b287218f7641))
* **db:** single key `room_id_is_running` enough ([c8dbae7](https://github.com/mynaparrot/plugNmeet-server/commit/c8dbae7336c949fd91fe871d7098fa7d89c6cf6d))
* **DI:** dependency inject improvement ([5573565](https://github.com/mynaparrot/plugNmeet-server/commit/55735653f78664ab2374edb45467c32a5f6fdcf3))
* **etherpad:** Make etherpad stateless to prevent race conditions ([7eb1d54](https://github.com/mynaparrot/plugNmeet-server/commit/7eb1d541438f21ba3748893abe1753ecaf22c370))
* **etherpad:** prevent to clean up if nodeId empty ([b7b50cb](https://github.com/mynaparrot/plugNmeet-server/commit/b7b50cbdc148b9ba5dfb350aaf8d97efd71f9fe5))
* **improve:** grouped routers + more logs ([2bb7100](https://github.com/mynaparrot/plugNmeet-server/commit/2bb7100f0fe29dcec6d04d48b7aa77e4901a12b2))
* **improve:** implemented better logging and other improvements ([13fd15b](https://github.com/mynaparrot/plugNmeet-server/commit/13fd15b61220ad81ee16ee6af0456ad87abae560))
* **improve:** Optimize Redis implementation for performance ([8cde76b](https://github.com/mynaparrot/plugNmeet-server/commit/8cde76b3716cf53271cd7e4808e9edab5a28f9bb))
* **logging:** added more logs ([012f04d](https://github.com/mynaparrot/plugNmeet-server/commit/012f04de0c4fe292181904501a1a5cce3de567a6))
* **logging:** implemented better logging ([b36b0d6](https://github.com/mynaparrot/plugNmeet-server/commit/b36b0d63553b610457c8807757e922177126496b))
* **nats:** Implement worker pool for message processing ([a38e8c9](https://github.com/mynaparrot/plugNmeet-server/commit/a38e8c95fc874f4890d460f8e6089d72100ad217))
* **poll:** backward incompatibility ([cdbf51c](https://github.com/mynaparrot/plugNmeet-server/commit/cdbf51ccc20008d61d0e6b53730842e8994674b9))
* rearranged room ended sequence + context aware ([02911a1](https://github.com/mynaparrot/plugNmeet-server/commit/02911a1be2137d61b310cd8cadff4a2e3ab67852))
* **refactor:** removed config global variable ([ed02e72](https://github.com/mynaparrot/plugNmeet-server/commit/ed02e72c14fde6c46d5a707f421635e74a20db89))
* **room:** fixed critical race condition while quick room end/create situation ([20011dd](https://github.com/mynaparrot/plugNmeet-server/commit/20011dd7caa66c6a85e3d93c8d7fb2e0793b2b2d))
* **room:** Hold temporary room data on exit for webhook race condition ([6a207bd](https://github.com/mynaparrot/plugNmeet-server/commit/6a207bd50b56cbdb2096210e7b49ddb1467b0011))
* use application context as parent + cleanup ([929ee66](https://github.com/mynaparrot/plugNmeet-server/commit/929ee6661af140db04ab257b5dcdfe51f19966b1))
* use application context when locking ([bc1293e](https://github.com/mynaparrot/plugNmeet-server/commit/bc1293e5687241dc52949f54716198d76dae8e15))
* use application level context ([af55c4b](https://github.com/mynaparrot/plugNmeet-server/commit/af55c4b299e48460906a33cf8cb05a2821484edd))
* use better way to check user's online status ([2b4963d](https://github.com/mynaparrot/plugNmeet-server/commit/2b4963df692230129fc34ffc6bc0c072bca87dd9))
* version display ([41cb9f2](https://github.com/mynaparrot/plugNmeet-server/commit/41cb9f24fdc0aeb6cbdaa0c07f647f284a9e860a))

## [1.9.2](https://github.com/mynaparrot/plugNmeet-server/compare/v1.9.1...v1.9.2) (2025-09-13)


### Bug Fixes

* bump dependencies ([f5e47e2](https://github.com/mynaparrot/plugNmeet-server/commit/f5e47e2b7901cb27d8751d23d8ad6326425343d9))
* dependencies upgrade ([f600715](https://github.com/mynaparrot/plugNmeet-server/commit/f6007152d3e528a8b490d704e78a6885a0fe907b))
* protocol update ([7398578](https://github.com/mynaparrot/plugNmeet-server/commit/7398578170d575923c9c93805c5c15eab2b657e0))
* update readme ([443043e](https://github.com/mynaparrot/plugNmeet-server/commit/443043eacaca187971d09a1e043dfe0017e192ac))

## [1.9.1](https://github.com/mynaparrot/plugNmeet-server/compare/v1.9.0...v1.9.1) (2025-08-12)


### Bug Fixes

* nil checker to avoid `nil pointer dereference` error ([0f5fc9d](https://github.com/mynaparrot/plugNmeet-server/commit/0f5fc9d25e911ccbed9394f8387ac620a5abf885))
* protocol upgrade ([5f7185f](https://github.com/mynaparrot/plugNmeet-server/commit/5f7185fd502f0f3cf651750571a7d7ffea353d1e))

## [1.9.0](https://github.com/mynaparrot/plugNmeet-server/compare/v1.8.4...v1.9.0) (2025-07-15)


### Features

* dependencies injection using wire ([0d6ddef](https://github.com/mynaparrot/plugNmeet-server/commit/0d6ddefc6b718d7c546ab0b582c21ed46062cf02))
* dependencies injection using wire ([a9e78ff](https://github.com/mynaparrot/plugNmeet-server/commit/a9e78ff03942386ce3d6177f8a7e8aabbd2f0a17))


### Bug Fixes

* cleaned up logic ([4aa5b49](https://github.com/mynaparrot/plugNmeet-server/commit/4aa5b4930286af2d5379cafc3b914bcf36cde083))
* **cleanup:** remove unnecessary goroutine ([b5f6d0a](https://github.com/mynaparrot/plugNmeet-server/commit/b5f6d0a82a232ad3d97ef82ffe4bfe47a86407ac))
* **deps:** update github.com/mynaparrot/plugnmeet-protocol digest to b2a5cc6 ([ce7e129](https://github.com/mynaparrot/plugNmeet-server/commit/ce7e12944665765bc7918d6fe1d5425bd413748a))
* **deps:** update github.com/mynaparrot/plugnmeet-protocol digest to b2a5cc6 ([3e40713](https://github.com/mynaparrot/plugNmeet-server/commit/3e4071398f9c55b6e305a242f789f0d53126e3e4))
* **deps:** update module github.com/ansrivas/fiberprometheus/v2 to v2.12.0 ([b5c2b26](https://github.com/mynaparrot/plugNmeet-server/commit/b5c2b26cc02fc0be77bbdf660350fccb9a0ab04e))
* **deps:** update module github.com/ansrivas/fiberprometheus/v2 to v2.12.0 ([3bda07e](https://github.com/mynaparrot/plugNmeet-server/commit/3bda07e3d1b7abd4f863547e21aca9a2245d03c6))
* **improve:** handle time way more efficiently ([c379ec2](https://github.com/mynaparrot/plugNmeet-server/commit/c379ec254cb0acf025eb1d7e32e71e0aa44b3601))
* **improve:** improve webhook notifier to handle each room's request in separate queue ([762809c](https://github.com/mynaparrot/plugNmeet-server/commit/762809c4db604251d62c5dc2bac39d6934562d80))
* **improve:** move `os.Signal` in bootup and remove unnecessary goroutine ([4eeed0c](https://github.com/mynaparrot/plugNmeet-server/commit/4eeed0cfedd7f18307b807066a92e1831e3e7be6))
* **improve:** optimized database query and added index ([d9c1354](https://github.com/mynaparrot/plugNmeet-server/commit/d9c135411b4af76bad8d86f4f05708e568d58014))
* **improve:** various improvement regarding file handling ([d4453fe](https://github.com/mynaparrot/plugNmeet-server/commit/d4453fe7535fd1604591dfa5256a7800b0d9cb0a))
* mistakenly removed post method ([13a6994](https://github.com/mynaparrot/plugNmeet-server/commit/13a69947066217a10ce9f58d82aa3cfd1f926fdb))

## [1.8.4](https://github.com/mynaparrot/plugNmeet-server/compare/v1.8.3...v1.8.4) (2025-06-27)


### Bug Fixes

* bug + clean up ([45bca50](https://github.com/mynaparrot/plugNmeet-server/commit/45bca50d19638a3f7e273d2a38f6d7fdd05f8bbc))
* bump proto ([acb2bbf](https://github.com/mynaparrot/plugNmeet-server/commit/acb2bbf9a0d80041483313c3404189a813795f1e))
* **deps:** protocol update ([a321514](https://github.com/mynaparrot/plugNmeet-server/commit/a32151457d29ff33ca8fd5be7a27f9f12a731c50))
* **deps:** update module github.com/ansrivas/fiberprometheus/v2 to v2.10.0 ([7364863](https://github.com/mynaparrot/plugNmeet-server/commit/7364863bad7628530fce3f17a8a40fca038a6ce0))
* **deps:** update module github.com/ansrivas/fiberprometheus/v2 to v2.10.0 ([cfbb9f3](https://github.com/mynaparrot/plugNmeet-server/commit/cfbb9f336e38382a4802a700b194f0cc92b5e81c))
* few more improvements + clean up ([689d676](https://github.com/mynaparrot/plugNmeet-server/commit/689d6762807e7e356da69c3a1d3b3df14578e882))
* few optimization and code clean up ([bb7eb7e](https://github.com/mynaparrot/plugNmeet-server/commit/bb7eb7ebfa7542d4a1bcaba03f75f2c14df6422d))
* **improve:** added better room creation lock system ([4353828](https://github.com/mynaparrot/plugNmeet-server/commit/4353828429cc23fde29fa66c6b4315c92096be9f))
* **improve:** added few more tests ([4893ebe](https://github.com/mynaparrot/plugNmeet-server/commit/4893ebe6c701028a203e2d64c24f2abe78564952))
* **improve:** added nats data cache feature ([7760b08](https://github.com/mynaparrot/plugNmeet-server/commit/7760b08fde40035fd0c6423c6c3cbea8a5f8a84b))
* **improve:** added nats data cache feature ([5502704](https://github.com/mynaparrot/plugNmeet-server/commit/5502704e69d64f5475a3c80a5475b19b80f1e6e8))
* updated text ([4081b07](https://github.com/mynaparrot/plugNmeet-server/commit/4081b0751d680fe3e3c8cab50922f45873fc34c0))

## [1.8.3](https://github.com/mynaparrot/plugNmeet-server/compare/v1.8.2...v1.8.3) (2025-05-05)


### Bug Fixes

* **bug:** ingress was removing because of invalid inactivity checking ([c08ec4a](https://github.com/mynaparrot/plugNmeet-server/commit/c08ec4ab3f060183c328d0d40a692ab8ab455ce6))
* **bug:** user's online status wasn't updated properly in the latest version of nats-server ([f9d9793](https://github.com/mynaparrot/plugNmeet-server/commit/f9d97932e902832455f7f4ce6e13bd8f702679ab))
* **deps:** deps update ([90d40b3](https://github.com/mynaparrot/plugNmeet-server/commit/90d40b3a6faa84da20889687d286fa62acb13b0e))
* **deps:** deps update ([a03f37e](https://github.com/mynaparrot/plugNmeet-server/commit/a03f37e3cc9efa3baf7c7866157460c27350c1b7))
* user appropriate library where necessary ([dcb7e6e](https://github.com/mynaparrot/plugNmeet-server/commit/dcb7e6e4c866ffab512e8fef9afab7e9fb5205b6))

## [1.8.2](https://github.com/mynaparrot/plugNmeet-server/compare/v1.8.1...v1.8.2) (2025-04-10)


### Bug Fixes

* added option to insert a self provided e2ee key ([386cbdd](https://github.com/mynaparrot/plugNmeet-server/commit/386cbdd1ce79e32ce206f18164fc7ed84abffc64))
* api to upload base64 encoded files ([f7b9923](https://github.com/mynaparrot/plugNmeet-server/commit/f7b992311ab4ef0a0214030444bb8fd1b420ca43))
* bump proto ([21b0c2d](https://github.com/mynaparrot/plugNmeet-server/commit/21b0c2de01356ad588d53244c51e17d788d98214))
* deps update ([264d0ee](https://github.com/mynaparrot/plugNmeet-server/commit/264d0eecf750ece8ebc77cd8361997003e4df019))
* **deps:** update module github.com/go-jose/go-jose/v4 to v4.0.5 [security] ([42be81b](https://github.com/mynaparrot/plugNmeet-server/commit/42be81ba43c996af7d0ee9bcb4cfe79b1c11fd21))
* **deps:** update module github.com/go-jose/go-jose/v4 to v4.0.5 [security] ([084bc36](https://github.com/mynaparrot/plugNmeet-server/commit/084bc367dbf9ac2fa021ccf0fb42caa651cbd01d))
* **deps:** update module github.com/redis/go-redis/v9 to v9.7.1 ([85b851b](https://github.com/mynaparrot/plugNmeet-server/commit/85b851baf731dbb9ac672750db36bdf59f2350a5))
* **deps:** update module github.com/redis/go-redis/v9 to v9.7.1 ([be23bbe](https://github.com/mynaparrot/plugNmeet-server/commit/be23bbe3e8efd00e5a20e00b2354cb6fc011a2f2))
* **deps:** update module github.com/redis/go-redis/v9 to v9.7.3 [security] ([6f845c7](https://github.com/mynaparrot/plugNmeet-server/commit/6f845c732f077f72629eeb20e9b980b72d2397e3))
* **deps:** update module github.com/redis/go-redis/v9 to v9.7.3 [security] ([607dac6](https://github.com/mynaparrot/plugNmeet-server/commit/607dac6f292b4ac644f9ae5af6cb645f9c362c9f))
* display runtime version too ([7ac5d23](https://github.com/mynaparrot/plugNmeet-server/commit/7ac5d23a29f686d969905b382a0803e5b77ede36))
* moved poll to its own features option with backward compatibility ([b361af4](https://github.com/mynaparrot/plugNmeet-server/commit/b361af4161d6b134be037084c86f8bbc3c252e1f))
* upgrade urfave/cli to `v3` ([887bc76](https://github.com/mynaparrot/plugNmeet-server/commit/887bc76ebb8a5df0410af1286772635e489613ea))

## [1.8.1](https://github.com/mynaparrot/plugNmeet-server/compare/v1.8.0...v1.8.1) (2025-02-20)


### Bug Fixes

* bump proto ([9fcde46](https://github.com/mynaparrot/plugNmeet-server/commit/9fcde46144b7a0511dc77d218276b5f32f99e97b))
* **deps:** update module github.com/bufbuild/protovalidate-go to v0.9.2 ([e924e7b](https://github.com/mynaparrot/plugNmeet-server/commit/e924e7b1f0bb481b367b62f794438b1e3b5098c1))
* just for safety ([17ba8f4](https://github.com/mynaparrot/plugNmeet-server/commit/17ba8f4eafbfc40982d533743a8f6285176e03bc))
* moved to proto for reusing ([517d3ad](https://github.com/mynaparrot/plugNmeet-server/commit/517d3ad38c3067964fda5318e9517a7380e4dfc7))
* option to use nkey ([9bd1964](https://github.com/mynaparrot/plugNmeet-server/commit/9bd196401a8bb09c792f4b93496564a7404682d3))
* optional for encrypting the nats request payloads ([4bfd3e9](https://github.com/mynaparrot/plugNmeet-server/commit/4bfd3e9d72982eb21f633a8682ac1056ee70125e))
* prevent using reserved name ([e5b3b13](https://github.com/mynaparrot/plugNmeet-server/commit/e5b3b13ec04e5d70a399ac326d81d32fdbfad434))
* recording server will authenticate with plugNmeet server for nats ([dee6082](https://github.com/mynaparrot/plugNmeet-server/commit/dee608242b7590e09d4a4c9a6515eb19e8c1167f))
* replacing with nkey for new installations ([30e1463](https://github.com/mynaparrot/plugNmeet-server/commit/30e146336d63c78b85f043caabb6f1be8528a708))
* some clean up ([2472ad3](https://github.com/mynaparrot/plugNmeet-server/commit/2472ad3ccabda7a7de0006a5deec60996675342f))
* use proper format ([a20d0b6](https://github.com/mynaparrot/plugNmeet-server/commit/a20d0b6b99016483d5ebf32eb96a9cb2a8066cad))

## [1.8.0](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.8...v1.8.0) (2025-02-01)


### Features

* option to keep deleted recordings in backup ([7d1ee5d](https://github.com/mynaparrot/plugNmeet-server/commit/7d1ee5db73d3689d7137233ba0dbe48294eb82a6))


### Bug Fixes

* deps update ([93fff85](https://github.com/mynaparrot/plugNmeet-server/commit/93fff85fe110fc79da5a602c51d2f91c8031e321))
* **deps:** update module github.com/goccy/go-json to v0.10.5 ([93ef56e](https://github.com/mynaparrot/plugNmeet-server/commit/93ef56eeeb28292a1d0409684bc71a624bd414aa))
* **deps:** update module github.com/livekit/server-sdk-go/v2 to v2.4.2 ([d689646](https://github.com/mynaparrot/plugNmeet-server/commit/d6896460b5184218044ba81fd4dfc7f6b9405235))
* few updates ([3944420](https://github.com/mynaparrot/plugNmeet-server/commit/394442074371516ac16ccef00a4ad70aa6d08d72))
* remove duplicate tag ([377903f](https://github.com/mynaparrot/plugNmeet-server/commit/377903f6347033da290c381c12540021259d201c))

## [1.7.8](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.7...v1.7.8) (2025-01-18)


### Bug Fixes

* bump nodejs ([2779eab](https://github.com/mynaparrot/plugNmeet-server/commit/2779eab3538a008d693e9a577b07aa78c5640a0e))
* **ci:** added beta release ([e7497e2](https://github.com/mynaparrot/plugNmeet-server/commit/e7497e2a84f3768007ecb1a787feea0ee87b883c))
* **ci:** always-update ([1134c50](https://github.com/mynaparrot/plugNmeet-server/commit/1134c50f91c84c7fc9eabf5e82f9b50ffdc01472))
* **ci:** bump beta version ([b79a0ea](https://github.com/mynaparrot/plugNmeet-server/commit/b79a0eadd93a0e06070f12dc087c4f18ec49247a))
* **ci:** bump beta version ([24c9082](https://github.com/mynaparrot/plugNmeet-server/commit/24c90821b4e9309ec6666ddcc19caf0f6069168d))
* **ci:** removed rebase again ([db27ad5](https://github.com/mynaparrot/plugNmeet-server/commit/db27ad54d16a4b39c9b5347ee2f06a8164a95266))
* **ci:** To rebase again ([e1681fc](https://github.com/mynaparrot/plugNmeet-server/commit/e1681fc79aede64a5a9cc297d7a4505461767eb8))
* **ci:** To rebase again ([161b04e](https://github.com/mynaparrot/plugNmeet-server/commit/161b04ed775d25a873617c229a0a8bfb23989672))
* **ci:** To rebase again ([4a34670](https://github.com/mynaparrot/plugNmeet-server/commit/4a3467056d97d57245219b0f7cb6d406c3a1fd92))
* deps update ([0d58c13](https://github.com/mynaparrot/plugNmeet-server/commit/0d58c1323a618e4fe6c2bdcb2fd853409851e3a5))
* **deps:** update module github.com/gabriel-vasile/mimetype to v1.4.8 ([703623d](https://github.com/mynaparrot/plugNmeet-server/commit/703623d55bbcd8a2b9c613dbe07446670e3f2ccb))
* **deps:** update module github.com/livekit/server-sdk-go/v2 to v2.4.1 ([89cac3f](https://github.com/mynaparrot/plugNmeet-server/commit/89cac3f4eed56abf6a60ac3ef51df88b62503738))
* evaluate response from recorder ([7b81c4a](https://github.com/mynaparrot/plugNmeet-server/commit/7b81c4a6bba042d2887fbdd1a872349a46850527))
* use `StatusServiceUnavailable` ([554e1f5](https://github.com/mynaparrot/plugNmeet-server/commit/554e1f5e8167ccf2bb91b796f5ffc2369f598908))

## [1.7.7](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.6...v1.7.7) (2024-12-20)


### Bug Fixes

* **bug:** Ingress was not working in the new Nats solution. Ref: https://github.com/mynaparrot/plugNmeet-server/discussions/611 ([2fc55a4](https://github.com/mynaparrot/plugNmeet-server/commit/2fc55a4e5c02787962a4e77804cfb83b38c7d975))
* **deps:** deps update ([3d8d4b9](https://github.com/mynaparrot/plugNmeet-server/commit/3d8d4b933cdb6fd8a992ddc672d22c83795a2be6))
* **deps:** upgrade `nats.go` ([bc3a121](https://github.com/mynaparrot/plugNmeet-server/commit/bc3a121a67920d5c082d6d6f6c4d2f570258f9bb))
* **update:** few clean ups ([3d632ec](https://github.com/mynaparrot/plugNmeet-server/commit/3d632ecd5f4394d5759b2ab8c89facf3ffeb5142))

## [1.7.6](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.5...v1.7.6) (2024-12-07)


### Bug Fixes

* **deps:** dependencies update ([ef83e0d](https://github.com/mynaparrot/plugNmeet-server/commit/ef83e0daaecb76dc884f53c4c5187d65619d3968))
* **deps:** update module github.com/nats-io/nkeys to v0.4.8 ([6b213d9](https://github.com/mynaparrot/plugNmeet-server/commit/6b213d95a7abacff4092e25fd3a9e4f8f2a3377e))
* missed to add `Replicas` ([fb6cba0](https://github.com/mynaparrot/plugNmeet-server/commit/fb6cba0c453c61797578662b56c2254263a52f5e))

## [1.7.5](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.4...v1.7.5) (2024-11-25)


### Bug Fixes

* added option `-trimpath` ([6b355d9](https://github.com/mynaparrot/plugNmeet-server/commit/6b355d9a89c668e400fefbcfea4dcfae54e14bee))
* **bump:** bump version ([de8f4dd](https://github.com/mynaparrot/plugNmeet-server/commit/de8f4dd1f0dd5086684738c7d762360cfccd28d8))
* clean up ([6e12ff1](https://github.com/mynaparrot/plugNmeet-server/commit/6e12ff16e7e5dea4e2c41c64dee5e1e8cdb13053))
* **deps:** update github.com/mynaparrot/plugnmeet-protocol digest to 4d9a8f6 ([d9ec4b4](https://github.com/mynaparrot/plugNmeet-server/commit/d9ec4b43c9abebe5ee05a59ffc9e01b417090691))
* **deps:** update module github.com/bufbuild/protovalidate-go to v0.7.3 ([0c51ba8](https://github.com/mynaparrot/plugNmeet-server/commit/0c51ba89ad3037b71bf5a3f7ae83272811c0a78e))
* **deps:** update module github.com/gabriel-vasile/mimetype to v1.4.7 ([56f5d3b](https://github.com/mynaparrot/plugNmeet-server/commit/56f5d3becfacba5c535cf45615c2fdeaf898274d))
* **deps:** update module github.com/livekit/protocol to v1.27.1 ([373f1ba](https://github.com/mynaparrot/plugNmeet-server/commit/373f1ba67264263816b1fb0a912f935d44781e5e))
* **deps:** update module github.com/livekit/server-sdk-go/v2 to v2.3.1 ([d5df05d](https://github.com/mynaparrot/plugNmeet-server/commit/d5df05db06f3f9644971bf8d75b90bf956fd6dd6))

## [1.7.4](https://github.com/mynaparrot/plugNmeet-server/compare/v1.7.3...v1.7.4) (2024-11-04)


### Bug Fixes

* **deps:** update module github.com/gabriel-vasile/mimetype to v1.4.6 ([fc6df57](https://github.com/mynaparrot/plugNmeet-server/commit/fc6df57253b8ff035cc87e3d76d651e2391a9419))
* **deps:** update module github.com/redis/go-redis/v9 to v9.7.0 ([8d7dc9a](https://github.com/mynaparrot/plugNmeet-server/commit/8d7dc9ad888af695cdcab2562e881d76b73b0a15))
* missing EmptyTimeout & MaxParticipants in webhook ([879d5b1](https://github.com/mynaparrot/plugNmeet-server/commit/879d5b1591553fc3c43f9f11b5ac3a8ad2d618d2))
* release-please-action ([3223400](https://github.com/mynaparrot/plugNmeet-server/commit/3223400ec426f66fd8bbf2628877e6801105be37))
