// @ts-check

import { inspect } from "util";

import { fs } from "zx";
import { program } from "commander";
import * as puppeteer from "puppeteer";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkRehype from "remark-rehype";
import rehypeStringify from "rehype-stringify";
import remarkGfm from "remark-gfm";
import rehypeMermaid from "rehype-mermaid";
import remarkMath from "remark-math";
import rehypeMathjax from "rehype-mathjax";
import { visit } from "unist-util-visit";
import * as markmap from "markmap-lib";
import * as markmapRender from "markmap-render";
import svg2img from "svg2img";
import { toHtml } from "hast-util-to-html";
import * as vega from "vega";
import * as vegaLite from "vega-lite";
import assert from "assert";

const CHROME_PATH = "/usr/bin/google-chrome";

/**
 * @typedef {Object} ConversionOpts
 * @property {boolean=} convertToImage
 * @property {number=} scaleFactor
 *
 * @typedef {Object} HtmlToImageOpts
 * @property {number=} width
 * @property {number=} height
 * @property {number=} scaleFactor
 *
 * @typedef {import("hast").Node} Node
 * @typedef {import("hast").Element} Element
 * @typedef {import("unified").Transformer<Node, Node>} Transformer
 */

/**
 * @param {Node} node
 * @returns {string}
 */
function inspectNode(node) {
  return inspect(node, { depth: 4 });
}

/**
 *
 * @param {string} html
 * @param {HtmlToImageOpts} opts
 * @returns {Promise<Buffer>}
 */
async function htmlToImage(html, { width = 2560, height = 1440, scaleFactor = 2 } = {}) {
  const browser = await puppeteer.launch({
    executablePath: CHROME_PATH,
    args: ["--no-sandbox", "--disable-setuid-sandbox"],
  });
  try {
    const page = await browser.newPage();
    page.setViewport({ width, height, deviceScaleFactor: scaleFactor });
    await page.setContent(html, { waitUntil: "networkidle0" });
    const element = await page.$("body");
    if (element == null) {
      throw new Error("Failed to find body element for screenshot");
    }
    const imageBuffer = await element.screenshot({ type: "png" });
    return Buffer.from(imageBuffer);
  } finally {
    await browser.close();
  }
}

/**
 * Create an error node.
 * @param {string} message
 * @param {Error} error
 * @returns {Element}
 */
function errorNode(message, error) {
  let text = message;
  if (error != null) {
    text += `\n\n${error.stack ?? error.message ?? String(error)}`;
  }

  return {
    type: "element",
    tagName: "pre",
    properties: { style: "color: red; text-wrap: wrap;" },
    children: [
      {
        type: "text",
        value: text,
      },
    ],
  };
}

/**
 *
 * @param {Element} node
 * @returns {boolean}
 */
function isMarkmapNode(node) {
  const firstChild = node.children ? node.children[0] : null;
  return (
    node.type === "element" &&
    node.tagName === "pre" &&
    firstChild?.type === "element" &&
    firstChild?.tagName === "code" &&
    Array.isArray(firstChild?.properties?.className) &&
    firstChild?.properties?.className?.includes("language-markmap")
  );
}

/**
 * Transform a Markmap node.
 * @param {Element} node
 * @param {ConversionOpts} opts
 * @returns
 */
async function transformMarkmapNode(node, { convertToImage = false, scaleFactor } = {}) {
  try {
    if (node.children[0].type !== "element" || node.children[0].children[0].type !== "text") {
      throw new Error(`Invalid markmap node structure: ${inspectNode(node)}`);
    }
    const transformer = new markmap.Transformer();
    const { root, features } = transformer.transform(node.children[0].children[0].value);
    const assets = transformer.getUsedAssets(features);
    const html = markmapRender.fillTemplate(root, assets, {
      jsonOptions: {
        duration: 0,
        maxInitialScale: 5,
      },
    });

    if (convertToImage) {
      const imageBuffer = await htmlToImage(html, { scaleFactor });
      const imageBase64 = imageBuffer.toString("base64");
      return {
        type: "element",
        tagName: "img",
        properties: {
          src: `data:image/png;base64,${imageBase64}`,
          style: "max-width: 100%; height: auto; display: block;",
        },
        children: [],
      };
    }

    return {
      type: "element",
      tagName: "iframe",
      properties: {
        srcdoc: html,
        style: "width: 100%; border: none;",
        sandbox: "allow-scripts allow-same-origin",
      },
      children: [],
    };
  } catch (error) {
    return errorNode("Failed to render markmap diagram.", error);
  }
}

/**
 *
 * @param {ConversionOpts} opts
 * @returns {import("unified").Transformer<Node, Node>}
 */
function rehypeMarkmap({ convertToImage = false, scaleFactor } = {}) {
  return async (tree) => {
    const markmapNodes = [];
    visit(tree, "element", (node, index, parent) => {
      if (isMarkmapNode(node)) {
        markmapNodes.push({ node, index, parent });
      }
    });
    await Promise.all(
      markmapNodes.map(async ({ node, index, parent }) => {
        parent.children[index] = await transformMarkmapNode(node, { convertToImage, scaleFactor });
      }),
    );
  };
}

/**
 *
 * @param {Element} node
 * @returns {boolean}
 */
function isVegaLiteNode(node) {
  const firstChild = node.children ? node.children[0] : null;
  return (
    node.type === "element" &&
    node.tagName === "pre" &&
    firstChild?.type === "element" &&
    firstChild?.tagName === "code" &&
    Array.isArray(firstChild?.properties?.className) &&
    firstChild?.properties?.className?.includes("language-vega-lite")
  );
}

/**
 * Transform a Vega-Lite node.
 * @param {Element} node
 * @returns {Promise<Element>}
 */
async function transformVegaLiteNode(node) {
  try {
    if (node.children[0].type !== "element" || node.children[0].children[0].type !== "text") {
      throw new Error(`Invalid Vega-Lite node structure: ${inspectNode(node)}`);
    }
    const spec = JSON.parse(node.children[0].children[0].value);
    spec.width = spec.width ?? 800;
    spec.height = spec.height ?? 600;
    const vegaSpec = vegaLite.compile(spec).spec;
    const view = new vega.View(vega.parse(vegaSpec), { renderer: "none" });
    const svg = await view.toSVG();
    const svgBase64 = Buffer.from(svg).toString("base64");
    return {
      type: "element",
      tagName: "img",
      properties: {
        src: `data:image/svg+xml;base64,${svgBase64}`,
        style: "max-width: 100%; height: auto; display: block;",
      },
      children: [],
    };
  } catch (error) {
    return errorNode("Failed to render Vega-Lite diagram.", error);
  }
}

/**
 * Check if a node is a Vega node.
 * @param {Element} node
 * @returns {boolean}
 */
function isVegaNode(node) {
  const firstChild = node.children ? node.children[0] : null;
  return (
    node.type === "element" &&
    node.tagName === "pre" &&
    firstChild?.type === "element" &&
    firstChild?.tagName === "code" &&
    Array.isArray(firstChild?.properties?.className) &&
    firstChild?.properties?.className?.includes("language-vega")
  );
}

/**
 * Transform a Vega node.
 * @param {Element} node
 * @returns {Promise<Element>}
 */
async function transformVegaNode(node) {
  try {
    if (node.children[0].type !== "element" || node.children[0].children[0].type !== "text") {
      throw new Error(`Invalid Vega node structure: ${inspectNode(node)}`);
    }
    const spec = JSON.parse(node.children[0].children[0].value);
    spec.width = spec.width ?? 800;
    spec.height = spec.height ?? 600;
    const view = new vega.View(vega.parse(spec), { renderer: "none" });
    const svg = await view.toSVG();
    const svgBase64 = Buffer.from(svg).toString("base64");
    return {
      type: "element",
      tagName: "img",
      properties: {
        src: `data:image/svg+xml;base64,${svgBase64}`,
        style: "max-width: 100%; height: auto; display: block;",
      },
      children: [],
    };
  } catch (error) {
    return errorNode("Failed to render Vega diagram.", error);
  }
}

/**
 * Rehype plugin to transform Vega-Lite nodes.
 * @returns {Transformer}
 */
function rehypeVega() {
  return async (tree) => {
    const vegaLiteNodes = [];
    const vegaNodes = [];
    visit(tree, "element", (node, index, parent) => {
      if (isVegaLiteNode(node)) {
        vegaLiteNodes.push({ node, index, parent });
      } else if (isVegaNode(node)) {
        vegaNodes.push({ node, index, parent });
      }
    });
    const promises = [];
    promises.push(
      ...vegaLiteNodes.map(async ({ node, index, parent }) => {
        parent.children[index] = await transformVegaLiteNode(node);
      }),
    );
    promises.push(
      ...vegaNodes.map(async ({ node, index, parent }) => {
        parent.children[index] = await transformVegaNode(node);
      }),
    );
    await Promise.all(promises);
  };
}

/**
 * Check if a node is an SVG image node.
 * @param {Element} node
 * @returns {boolean}
 */
function isSvgImageNode(node) {
  if (
    node.type === "element" &&
    node.tagName === "img" &&
    typeof node.properties.src === "string" &&
    node.properties.src.startsWith("data:image/svg+xml;base64,")
  )
    return true;
  if (node.type === "element" && node.tagName === "svg") return true;
  return false;
}

/**
 * Extract the SVG content from an image or SVG node.
 * @param {Element} node
 * @returns {string|null}
 */
function extractSvgContent(node) {
  if (node.tagName === "img") {
    return String(node.properties.src);
  } else if (node.tagName === "svg") {
    return toHtml(node);
  }
  return null;
}

/**
 * Convert SVG images to PNG format.
 * @param {ConversionOpts} opts
 * @returns {import("unified").Transformer<Node, Node>}
 */
function rehypeSvg2Png({ convertToImage = false }) {
  return async (tree) => {
    if (!convertToImage) return;

    const svgNodes = [];
    visit(tree, "element", (node, index, parent) => {
      if (isSvgImageNode(node)) {
        svgNodes.push({ node, index, parent, content: extractSvgContent(node) });
      }
    });
    await Promise.all(
      svgNodes.map(async ({ node, index, parent, content }) => {
        try {
          const buffer = await new Promise((resolve, reject) => {
            svg2img(content, (error, buffer) => {
              if (error) return reject(error);
              resolve(buffer);
            });
          });

          const imageBase64 = buffer.toString("base64");

          parent.children[index] = {
            type: "element",
            tagName: "img",
            properties: {
              src: `data:image/png;base64,${imageBase64}`,
              style: "max-width: 100%; height: auto; display: block;",
            },
            children: [],
          };
        } catch (error) {
          parent.children[index] = errorNode("Failed to convert SVG to PNG.", error);
        }
      }),
    );
  };
}

/**
 * Convert \[...\] to $$...$$ and \( ... \) to $...$.
 * @param {string} input
 * @returns {string}
 */
function preprocessMath(input) {
  return input
    .replace(/\\\[(.+?)\\\]/gms, "$$$$$1$$$$") // \[...\] to $$...$$
    .replace(/\\\((.+?)\\\)/gs, "$$$1$$"); // \( ... \) to $...$
}

async function main() {
  program
    .option("--convert-to-image", "Convert diagrams to images instead of embedding interactive content", false)
    .option("--convert-latex-to-image", "Convert LaTeX math to images", false)
    .option("--preprocess-math", "Preprocess math delimiters", false)
    .option("--scale-factor <factor>", "Scale factor for image conversion", parseFloat, 2)
    .argument("<input>", "Input markdown file path")
    .argument("<output>", "Output markdown file path");
  program.parse();

  const [inputPath, outputPath] = program.args;

  const opts = program.opts();
  const { convertToImage, convertLatexToImage, scaleFactor, preprocessMath: preprocessMathOpt } = opts;

  assert(typeof convertToImage === "boolean", "convertToImage must be a boolean");
  assert(typeof convertLatexToImage === "boolean", "convertLatexToImage must be a boolean");
  assert(typeof preprocessMathOpt === "boolean", "preprocessMath must be a boolean");
  assert(
    typeof scaleFactor === "number" && !isNaN(scaleFactor) && scaleFactor > 0,
    "scaleFactor must be a positive number",
  );

  let input = await fs.readFile(inputPath, "utf-8");
  if (preprocessMathOpt) {
    input = preprocessMath(input);
  }

  /**
   * @type {any[]}
   */
  const plugins = [
    [remarkParse],
    [remarkRehype],
    [remarkGfm],
    [remarkMath],
    [
      rehypeMermaid,
      {
        launchOptions: {
          executablePath: CHROME_PATH,
        },
        mermaidConfig: {
          flowchart: { htmlLabels: false },
          htmlLabels: false,
        },
      },
    ],
    [rehypeMarkmap, { convertToImage, scaleFactor }],
    [rehypeVega],
    [rehypeStringify],
  ];

  if (convertToImage && !convertLatexToImage) {
    plugins.push(
      [rehypeSvg2Png, { convertToImage: true, scaleFactor }], //
      [rehypeMathjax],
    );
  } else if (convertLatexToImage) {
    plugins.push(
      [rehypeMathjax], //
      [rehypeSvg2Png, { convertToImage: true, scaleFactor }],
    );
  } else {
    plugins.push([rehypeMathjax]);
  }

  let processor = unified();
  for (const [plugin, ...pluginArgs] of plugins) {
    processor = processor.use(plugin, ...pluginArgs);
  }
  const output = await processor.process(input);

  await fs.writeFile(outputPath, String(output), "utf-8");
}
main();
