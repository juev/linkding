'use strict';

document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('changelist-form');
  const toggle = document.getElementById('action-toggle');
  if (!form || !toggle) return;

  const checkboxes = Array.from(form.querySelectorAll('input[name="_selected_action"]'));
  const counter = form.querySelector('.action-counter');
  const across = form.querySelector('input[name="select_across"]');
  const question = form.querySelector('.question');
  const all = form.querySelector('.all');
  const clear = form.querySelector('.clear');

  function resetAcross() {
    across.value = '0';
    all?.classList.add('hidden');
    clear?.classList.add('hidden');
    counter?.classList.remove('hidden');
  }

  function update() {
    const selected = checkboxes.filter(checkbox => checkbox.checked).length;
    toggle.checked = checkboxes.length > 0 && selected === checkboxes.length;
    toggle.indeterminate = selected > 0 && selected < checkboxes.length;
    checkboxes.forEach(checkbox => checkbox.closest('tr')?.classList.toggle('selected', checkbox.checked));
    if (counter) counter.textContent = `${selected} of ${counter.dataset.actionsIcnt} selected`;
    if (across.value !== '1') {
      question?.classList.toggle('hidden', !toggle.checked);
      resetAcross();
    }
  }

  toggle.addEventListener('change', () => {
    resetAcross();
    checkboxes.forEach(checkbox => { checkbox.checked = toggle.checked; });
    update();
  });
  checkboxes.forEach(checkbox => checkbox.addEventListener('change', () => {
    resetAcross();
    update();
  }));
  question?.querySelector('a')?.addEventListener('click', event => {
    event.preventDefault();
    across.value = '1';
    question.classList.add('hidden');
    all?.classList.remove('hidden');
    clear?.classList.remove('hidden');
    counter?.classList.add('hidden');
  });
  clear?.querySelector('a')?.addEventListener('click', event => {
    event.preventDefault();
    checkboxes.forEach(checkbox => { checkbox.checked = false; });
    resetAcross();
    update();
  });
  update();
});
