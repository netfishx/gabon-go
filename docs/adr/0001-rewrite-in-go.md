# 用 Go 重写 gabon

旧版是 Java 17 / Spring Boot 3.2 / MyBatis-Plus 的三模块 Maven 工程（~19k 行）。重写目标是换技术栈、获得一份干净实现、并评估 AI 驱动全量重写的能力，因此选择 Go 而非 Kotlin/Spring（换汤不换药）或 TypeScript/Bun（与"后端复杂度高直接用 Spring/Go"的既有原则相悖）。单二进制部署、显式纹理、低资源占用也符合本项目单实例实验的形态。

## Considered Options

- Kotlin + Spring Boot：迁移风险最低，但仍在 Spring 生态内，"换栈"的评估信息量有限
- TypeScript + Bun (Elysia)：与前端栈统一，但后端复杂度（资金、状态机、转码管线）不适合
- Java 21 + Spring Boot 现代化：不算换栈，放弃
