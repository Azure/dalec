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
      items: [
        'intro',
        'build',
        'build-source',
        'virtual-packages',
      ],
    },
    {
      type: 'category',
      label: 'Specifications',
      items: [
        'spec',
        'sources',
        'targets',
        'testing',
      ],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'signing',
        'editor-support',
      ],
    },
  ],
};

export default sidebars;
