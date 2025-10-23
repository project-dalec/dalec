import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */
const sidebars: SidebarsConfig = {
  sidebar: [
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        'overview',
        'quickstart',
        'container-only-builds',
        'virtual-packages',
        'system-extensions',
      ],
    },
    {
      type: 'category',
      label: 'Specifications',
      collapsed: false,
      items: [
        'spec',
        'sources',
        'dependencies',
        'targets',
        'testing',
        'artifacts',
        'repositories',
        'caches'
      ],
    },
    {
      type: 'category',
      label: 'Features',
      collapsed: false,
      items: [
        'signing',
        'editor-support',
      ],
    },
    {
      type: 'category',
      label: 'Contributing',
      collapsed: false,
      items: [
        'architecture',
        'developers',
      ],
    },
  ],
};

export default sidebars;
