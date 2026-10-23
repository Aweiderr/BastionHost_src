# 堡垒机产品文档工程

本仓库包含堡垒机产品的四份交付文档及其生成工程。

## 目录结构

```
堡垒机/
├── docs/                       交付文档（对外输出）
│   ├── 堡垒机系统规划文档.docx          V1.1 · 78 页
│   ├── 堡垒机系统需求文档.docx          V1.1 · 116 页
│   ├── 堡垒机详细设计文档.docx          V1.0 · 167 页
│   ├── 堡垒机数据库设计文档.docx        V1.0 · 84 页
│   ├── 堡垒机产品文档评审与功能建议.md   外部评审意见（整改依据）
│   └── 堡垒机.txt                     原始需求
├── src/                        产品开发源码（待开发）
│   ├── README.md                 开发目录规划说明
│   └── .gitkeep
└── tools/                      文档生成工程（源码与工具链）
    ├── lib/                    生成引擎与数据源
    ├── assets/                 插图产物（PNG）与源文件（SVG）
    ├── content-*.js            文档正文（规划 / 需求 / 详设 / 数据库）
    ├── figs-*.js               插图脚本（SVG 绘制）
    ├── build-*.js              构建入口
    ├── verify*.js              校验脚本
    ├── review*.js              体检脚本
    ├── gen-reqdata.js          需求数据源生成
    └── README.md               工程说明（含全部变更记录）
```

## 四份文档的关系

```
规划文档 ──定架构──→ 需求文档 ──定验收──→ 详细设计文档 ──定实现──→ 数据库设计文档
（怎么建）           （验收什么）          （怎么实现）            （怎么存）
```

详细设计文档第 5 章引用数据库设计文档，不重复表结构定义。

## 文档规模

| 文档 | 版本 | 页数 | 表格 | 插图 |
| --- | --- | --- | --- | --- |
| 系统规划文档 | V1.1 | 78 | 69 | 11 |
| 系统需求文档 | V1.1 | 116 | 64 | 8 |
| 详细设计文档 | V1.0 | 167 | 244 | 11 |
| 数据库设计文档 | V1.0 | 84 | 88 | 4 |

需求规模：功能需求 **121 条**（P0 86 / P1 34 / P2 1）、初始化需求 30 条、非功能需求 66 条；数据库 **48 张业务表**。

## 构建方式

```powershell
cd tools

# 1. 若修改了需求条目，先重建需求数据源
node gen-reqdata.js

# 2. 生成四份文档（含插图渲染，输出到 ../docs/）
node build-plan.js
node build-req.js
node build-dd.js
node build-db.js

# 3. 更新目录页码（需本机安装 WPS）
#    pwsh -File update-toc.ps1 -Docx "D:\work\堡垒机\docs\堡垒机系统规划文档.docx"

# 4. 校验
node review.js     # 结构化体检：编号、表号、图号、占位符、角色词、层级
node review2.js    # 综合体检：语法、Markdown 残留、空内容、重复、交叉引用
node verify4.js    # 四份文档完整性
node verify5.js    # 评审整改验证（41 项）
node verify6.js    # 功能补充验证（31 项）
```

## 技术栈

- **文档生成**：Node.js + `docx` 库生成 OOXML
- **插图**：SVG 脚本 → 无头 Chrome 渲染为 2 倍分辨率 PNG
- **目录与分页**：WPS COM 接口（`TablesOfContents.Update()`）
- **需求数据源**：`gen-reqdata.js` 从正文提取，生成 `lib/reqdata.js` 供各表引用（杜绝手抄不一致）

详见 `tools/README.md`。
